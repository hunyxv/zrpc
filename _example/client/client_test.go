package main

import (
	"context"
	"errors"
	"example"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	zrpcCli "github.com/hunyxv/zrpc/client"
	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

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

// TestReqRepFunc 测试请求响应型方法
func TestReqRepFunc(t *testing.T) {
	// jaeger TracerProvider
	tp, err := tracerProvider("http://localhost:14268/api/traces")
	if err != nil {
		t.Fatal(err)
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
	_, span := tr.Start(ctx, "send reqrep")
	defer span.End()

	// 带链路追踪信息的 ctx
	ctx = trace.ContextWithSpan(ctx, span)

	id := "test-client" + fmt.Sprintf("%d", time.Now().UnixNano())
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)

	err = soc.Connect("tcp://127.0.0.1:10080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:10080")

	now := time.Now()
	// 提取出链路追踪所需数据，并绑定到 zrpc.Context
	ctx = &zrpc.Context{
		Context: zrpc.InjectTrace2ctx(ctx),
	}

	// 序列化 zrpc.Context
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	pack := &zrpc.Pack{
		Identity: id,
		Header:   make(zrpc.Header),
		Stage:    zrpc.REQUEST,
		Args:     [][]byte{rawCtx},
	}
	pack.SetMethodName("sayhello/Hello")
	pack.Set(zrpc.MESSAGEID, zrpc.NewMessageID())

	// 发送请求，接收响应
	r := client(soc, pack)

	// 反序列化响应
	var result zrpc.Pack
	err = msgpack.Unmarshal(r[0], &result)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(result)

	// 发送了异常
	if result.Stage == zrpc.ERROR {
		var e string
		msgpack.Unmarshal(result.Args[0], &e)
		t.Logf("ERR: %s", errors.New(e))
		return
	}

	var world string
	msgpack.Unmarshal(result.Args[0], &world)
	var e string
	msgpack.Unmarshal(result.Args[1], &e)
	t.Log("result: ", world, errors.New(e))
	t.Logf("takes %s", time.Since(now))
}

func client(soc *zmq.Socket, pack *zrpc.Pack) [][]byte {
	rawPack, err := msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}
	log.Println(string(rawPack))

	total, err := soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}
	log.Println("total: ", total)

	msg, err := soc.RecvMessageBytes(0)
	if err != nil {
		panic(err)
	}
	//log.Println("msg: ", msg, string(msg[0]))
	return msg
}

func TestReqRepCli(t *testing.T) {
	cli, err := zrpcCli.NewDirectClient(zrpcCli.ServerInfo{
		ServerName:    "example",
		NodeID:        "1111-111111-11111111",
		LocalEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10080},
		StateEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10082},
	})
	if err != nil {
		t.Fatal(err)
	}
	go cli.Run()
	defer cli.Close()

	proxy := &example.SayHelloProxy{}
	// 对代理对象函数进行替换
	err = cli.Decorator("sayhello", proxy, 3)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)

	// 调用
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := proxy.Hello(ctx)
	if err != nil {
		t.Log("测试返回错误： ", err)
	}

	t.Log(resp)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	resp2, err := proxy.Hello2(ctx2, &example.Resp{Name: "XiaoMing"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", resp2.Name)
}
