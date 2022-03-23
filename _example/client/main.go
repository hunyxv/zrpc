package main

import (
	"bufio"
	"context"
	"encoding/json"
	"example"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

var etcdEndpoint = flag.String("etcd", "", "etcd endpoint")
var methodName = flag.String("fname", "SayHello", "called method function name")

func main() {
	flag.Parse()

	ips, err := getLocalIps()
	if err != nil {
		panic(err)
	}
	// zrpc client
	var cli *zrpcCli.ZrpcClient
	if *etcdEndpoint == "" {
		cli, err = zrpcCli.NewDirectClient(zrpcCli.ServerInfo{
			ServerName:    "example",
			NodeID:        "1111-111111-11111111",
			LocalEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: ips[0], Port: 10080},
			StateEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: ips[0], Port: 10082},
		})
		// 启动后先 sleep 100 ms
		time.Sleep(100 * time.Millisecond)
	} else {
		discover, err := zrpc.NewEtcdDiscover(&zrpc.DiscoverConfig{
			Registries:    []string{*etcdEndpoint},
			ServicePrefix: "/zrpc",
			ServiceName:   zrpc.DefaultNode.ServiceName,
		})
		if err != nil {
			log.Fatal(err)
		}
		cli, err = zrpcCli.NewClient(discover)
		// 启动后先 sleep 100 ms
		time.Sleep(1 * time.Second)
	}

	if err != nil {
		log.Fatal(err)
	}
	defer cli.Close()

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
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
				bufw := bufio.NewWriter(writerCloser) // 使用写缓存，减少数据包的传递
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < count; i++ {
						fmt.Fprintf(bufw, "line %d\n", i)
					}
					bufw.Flush()
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
					r := bufio.NewReader(readerCloser) // 读缓存
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
			case "Stream":
				count := 100
				readerCloser, writerCloser := io.Pipe()
				readerCloser2, writerCloser2 := io.Pipe()
				rw := readeWriteCloser{
					Reader: readerCloser,
					Writer: writerCloser2,
					Closer: writerCloser,
				}

				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer rw.Close()
					r := bufio.NewReader(readerCloser2)
					for {
						data, _, err := r.ReadLine()
						if err != nil {
							return
						}
						var req example.RequestRespone
						json.Unmarshal(data, &req)
						log.Printf("req: %+v", req)
						data = append(data, '\n')
						writerCloser.Write(data)
					}
				}()
				err := proxy.Stream(ctx, count, rw)
				if err != nil {
					time.Sleep(time.Millisecond)
					log.Fatal(err)
				}
			default:
			}
		}()
	}
	wg.Wait()
	log.Println(time.Since(now))
}

type readeWriteCloser struct {
	io.Reader
	io.Writer
	io.Closer
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

func getLocalIps() ([]string, error) {
	interfaceAddr, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	ips := []string{}
	for _, addr := range interfaceAddr {
		ipNet, isVailIpNet := addr.(*net.IPNet)
		if isVailIpNet && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	return ips, nil
}
