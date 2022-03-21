package testdata

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	zrpcCli "github.com/hunyxv/zrpc/client"
)

var cli *zrpcCli.ZrpcClient
var once sync.Once

func Init() (*zrpcCli.ZrpcClient, error) {
	var err error
	once.Do(func() {
		cli, err = zrpcCli.NewDirectClient(zrpcCli.ServerInfo{
			ServerName:    "testdata",
			NodeID:        "22222222-2222-2222-2222-222222222222",
			LocalEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10090},
			StateEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10092},
		})
		if err != nil {
			return
		}
		// defer cli.Close()
		time.Sleep(time.Millisecond * 100)

		err = cli.Decorator("sayhello", new(SayHelloProxy), 3)
		if err != nil {
			return
		}
	})

	return cli, err
}

func TestReqRepClient(t *testing.T) {
	cli, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	proxy, ok := cli.GetSerivce("sayhello")
	if !ok {
		t.Fail()
	}

	sayHello := proxy.(*SayHelloProxy)
	sayHello.Hello(context.Background(), &User{Name: "Hunyxv"})
}

func BenchmarkReqRep(b *testing.B) {
	cli, err := Init()
	if err != nil {
		b.Fatal(err)
	}

	proxy, ok := cli.GetSerivce("sayhello")
	if !ok {
		b.Fail()
	}

	sayHello := proxy.(*SayHelloProxy)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sayHello.Hello(context.Background(), &User{Name: strconv.Itoa(i)})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReqRepParallel(b *testing.B) {
	cli, err := Init()
	if err != nil {
		b.Fatal(err)
	}

	proxy, ok := cli.GetSerivce("sayhello")
	if !ok {
		b.Fail()
	}

	sayHello := proxy.(*SayHelloProxy)

	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		var i int
		for p.Next() {
			i++
			_, err := sayHello.Hello(context.Background(), &User{Name: strconv.Itoa(i)})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
