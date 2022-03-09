package testdata

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
)

func TestRunserverWithRegistry1(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	// 为了测试，节点 id 设置为 111...
	zrpc.DefaultNode.NodeID = "11111111-1111-1111-1111-111111111111"

	registry, err := zrpc.NewEtcdRegistry(&zrpc.RegisterConfig{
		Registries:      []string{"127.0.0.1:2379"},
		ServicePrefix:   "/zrpc",
		HeartBeatPeriod: 5 * time.Second,
		ServerInfo:      zrpc.DefaultNode,
	})
	if err != nil {
		t.Fatal(err)
	}
	discover, err := zrpc.NewEtcdDiscover(&zrpc.DiscoverConfig{
		Registries:    []string{"127.0.0.1:2379"},
		ServicePrefix: "/zrpc",
		ServiceName:   zrpc.DefaultNode.ServiceName,
	})
	if err != nil {
		t.Fatal(err)
	}

	rd := zrpc.NewRegisterDiscover(registry, discover)

	go zrpc.Run(zrpc.WithRegisterDiscover(rd))
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

func TestRunserverWithRegistry2(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	Node := zrpc.Node{
		ServiceName:     "testdata",
		NodeID:          "22222222-2222-2222-2222-222222222222",
		LocalEndpoint:   "tcp://0.0.0.0:9080",
		ClusterEndpoint: "tcp://0.0.0.0:9081",
		StateEndpoint:   "tcp://0.0.0.0:9082",
		IsIdle:          true,
	}

	registry, err := zrpc.NewEtcdRegistry(&zrpc.RegisterConfig{
		Registries:      []string{"127.0.0.1:2379"},
		ServicePrefix:   "/zrpc",
		HeartBeatPeriod: 5 * time.Second,
		ServerInfo:      Node,
	})
	if err != nil {
		t.Fatal(err)
	}
	discover, err := zrpc.NewEtcdDiscover(&zrpc.DiscoverConfig{
		Registries:    []string{"127.0.0.1:2379"},
		ServicePrefix: "/zrpc",
		ServiceName:   zrpc.DefaultNode.ServiceName,
	})
	if err != nil {
		t.Fatal(err)
	}

	rd := zrpc.NewRegisterDiscover(registry, discover)

	go zrpc.Run(zrpc.WithRegisterDiscover(rd), zrpc.WithNodeInfo(Node))
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}
