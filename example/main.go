package main

import (
	"context"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/hunyxv/zrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type ISayHello interface {
	Hello(ctx context.Context) (string, error)
}

var _ ISayHello = (*SayHello)(nil)

type SayHello struct{}

func (s *SayHello) Hello(ctx context.Context) (string, error) {
	log.Println("Hello world!")
	time.Sleep(100 * time.Millisecond)
	getIP(ctx)
	return "world", errors.New("a error")
}

func main() {
	// 初始化 tp
	tp, err := tracerProvider("http://localhost:14268/api/traces")
	if err != nil {
		panic(err)
	}

	// 绑定全局 TracerProvider
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var i *ISayHello
	// 注册服务
	err = zrpc.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		panic(err)
	}
	// 启动服务
	go zrpc.Run()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

const (
	service     = "trace-demo"
	environment = "production"
	id          = 1
)

func tracerProvider(url string) (*tracesdk.TracerProvider, error) {
	// Create the Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(url)))
	if err != nil {
		return nil, err
	}
	tp := tracesdk.NewTracerProvider(
		// Always be sure to batch in production.
		tracesdk.WithBatcher(exp),
		// Record information about this application in a Resource.
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(service),
			attribute.String("environment", environment),
			attribute.Int64("ID", id),
		)),
	)
	return tp, nil
}

func isNil(i interface{}) bool {
	vi := reflect.ValueOf(i)
	if vi.Kind() == reflect.Ptr {
		return vi.IsNil()
	}
	return false
}

func getIP(ctx context.Context) {
	client := http.DefaultClient
	req, _ := http.NewRequest("GET", "http://icanhazip.com", nil)
	ctx, span := otel.GetTracerProvider().Tracer("SayHello").Start(ctx, "getIP")
	defer span.End()

	req = req.WithContext(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	res, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return
	}
	log.Printf("Received result: %s\n", body)
}
