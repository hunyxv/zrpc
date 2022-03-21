package main

import (
	"example"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hunyxv/zrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

var etcdEndpoint = flag.String("etcd", "", "etcd endpoint")

func main() {
	flag.Parse()
	// 初始化 tp
	tp, err := tracerProvider("http://localhost:14268/api/traces")
	if err != nil {
		log.Fatal(err)
	}

	// 绑定全局 TracerProvider
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var i *example.ISayHello
	// 注册服务
	err = zrpc.RegisterServer("sayhello", &example.SayHello{}, i)
	if err != nil {
		log.Fatal(err)
	}
	if *etcdEndpoint == "" {
		zrpc.DefaultNode.NodeID = "1111-111111-11111111"
		// 启动服务
		go zrpc.Run()
	} else {
		registry, err := zrpc.NewEtcdRegistry(&zrpc.RegisterConfig{
			Registries:      []string{*etcdEndpoint},
			ServicePrefix:   "/zrpc",
			HeartBeatPeriod: 5 * time.Second,
			ServerInfo:      zrpc.DefaultNode,
		})
		if err != nil {
			log.Fatal(err)
		}
		discover, err := zrpc.NewEtcdDiscover(&zrpc.DiscoverConfig{
			Registries:    []string{*etcdEndpoint},
			ServicePrefix: "/zrpc",
			ServiceName:   zrpc.DefaultNode.ServiceName,
		})
		if err != nil {
			log.Fatal(err)
		}

		go zrpc.Run(zrpc.WithRegisterDiscover(zrpc.NewRegisterDiscover(registry, discover)))
	}

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
