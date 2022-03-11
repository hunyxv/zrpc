package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

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