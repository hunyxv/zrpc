package main

import (
	"bufio"
	"context"
	"example"
	"flag"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/hunyxv/zrpc"
	zrpcCli "github.com/hunyxv/zrpc/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

var methodName = flag.String("fname", "SayHello", "called method function name")

func main() {
	flag.Parse()
	// zrpc client
	cli, err := zrpcCli.NewDirectClient(zrpcCli.ServerInfo{
		ServerName:    "example",
		NodeID:        "1111-111111-11111111",
		LocalEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10080},
		StateEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10082},
	})
	if err != nil {
		log.Fatal(err)
	}
	// 启动client
	go cli.Run()
	defer cli.Close()
	// 启动后先 sleep 100 ms
	time.Sleep(100 * time.Millisecond)

	// trace 相关的：
	tp, err := tracerProvider("http://localhost:14268/api/traces")
	if err != nil {
		log.Fatal(err)
	}

	// 绑定全局 TracerProvider
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func(ctx context.Context) {
		// Do not make the application hang when it is shutdown.
		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			log.Fatal(err)
		}
	}(ctx)

	tr := tp.Tracer("test-trace")

	// 对代理对象函数进行替换
	proxy := &example.SayHelloProxy{}
	err = cli.Decorator("sayhello", proxy, 3)
	if err != nil {
		log.Fatal(err)
	}

	// span start
	_, span := tr.Start(ctx, "send reqrep")
	defer span.End()
	// 带链路追踪信息的 ctx
	ctx = trace.ContextWithSpan(ctx, span)

	// 调用rpc服务
	ctx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	log.Println("method name: ", *methodName)
	switch *methodName {
	case "SayHello":
		resp, err := proxy.SayHello(ctx, "hunyxv")
		if err != nil {
			log.Println("测试返回错误： ", err)
			span.SetStatus(codes.Error, err.Error())
		}
		log.Println(resp)
	case "YourName":
		resp, err := proxy.YourName(ctx)
		if err != nil {
			log.Fatal("发生错误： ", err)
			span.SetStatus(codes.Error, err.Error())
		}
		log.Println(resp)
	case "StreamReq":
		count := 100
		readerCloser, writerCloser := io.Pipe()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < count; i++ {
				fmt.Fprintf(writerCloser, "line %d\n", i)
			}
			writerCloser.Close()
		}()
		resp, err := proxy.StreamReq(ctx, count, readerCloser)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("succ? ", resp)
		wg.Wait()
	case "StreamRep":
		count := 100
		readerCloser, writerCloser := io.Pipe()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := bufio.NewReader(readerCloser)
			for {
				data, _, err := r.ReadLine()
				if err != nil {
					return
				}
				log.Println(string(data))
			}
		}()
		err := proxy.StreamRep(ctx, count, writerCloser)
		if err != nil {
			log.Fatal(err)
		}
		wg.Wait()
	default:
	}
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