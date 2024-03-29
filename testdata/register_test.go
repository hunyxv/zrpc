package testdata

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
)

var rdType = flag.String("rd", "etcd", "registry discover type")

func newRegisterDiscover(node zrpc.Node) (zrpc.RegisterDiscover, error) {
	fmt.Println(*rdType)
	switch *rdType {
	case "etcd":
		registry, err := zrpc.NewEtcdRegistry(&zrpc.RegisterConfig{
			Registries:      []string{"127.0.0.1:2379"},
			ServicePrefix:   "/zrpc",
			HeartBeatPeriod: 5 * time.Second,
			ServerInfo:      node,
		})
		if err != nil {
			return nil, err
		}
		discover, err := zrpc.NewEtcdDiscover(&zrpc.DiscoverConfig{
			Registries:    []string{"127.0.0.1:2379"},
			ServicePrefix: "/zrpc",
			ServiceName:   node.ServiceName,
		})
		if err != nil {
			return nil, err
		}

		return zrpc.NewRegisterDiscover(registry, discover), nil
	case "consul":
		registry, err := zrpc.NewConsulRegister(&zrpc.RegisterConfig{
			Registries:      []string{"127.0.0.1:8500"},
			ServicePrefix:   "/zrpc",
			HeartBeatPeriod: 5 * time.Second,
			ServerInfo:      node,
		})
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		discover, err := zrpc.NewConsulDiscover(&zrpc.DiscoverConfig{
			Registries:    []string{"127.0.0.1:8500"},
			ServicePrefix: "/zrpc",
			ServiceName:   node.ServiceName,
		})
		if err != nil {
			return nil, err
		}
		return zrpc.NewRegisterDiscover(registry, discover), nil
	case "zk":
		registry, err := zrpc.NewZookeeperRegister(&zrpc.RegisterConfig{
			Registries:      []string{"127.0.0.1:2181"},
			ServicePrefix:   "/zrpc",
			HeartBeatPeriod: 5 * time.Second,
			ServerInfo:      node,
		})
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		discover, err := zrpc.NewZookeeperDiscover(&zrpc.DiscoverConfig{
			Registries:    []string{"127.0.0.1:2181"},
			ServicePrefix: "/zrpc",
			ServiceName:   node.ServiceName,
		})
		if err != nil {
			return nil, err
		}
		return zrpc.NewRegisterDiscover(registry, discover), nil
	}
	return nil, errors.New("unknow err")
}

func TestRunserverWithRegistry1(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("/sayhello", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	// 为了测试，节点 id 设置为 111...
	node := zrpc.DefaultNode
	node.NodeID = "11111111-1111-1111-1111-111111111111"

	rd, err := newRegisterDiscover(node)
	if err != nil {
		t.Fatal(err)
	}

	go zrpc.Run(zrpc.WithRegisterDiscover(rd), zrpc.WithNodeInfo(node))
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

func TestRunserverWithRegistry2(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("/sayhello", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	node := zrpc.Node{
		ServiceName:     "testdata",
		NodeID:          "22222222-2222-2222-2222-222222222222",
		LocalEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10090},
		ClusterEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10091},
		StateEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10092},
		IsIdle:          true,
	}

	rd, err := newRegisterDiscover(node)
	if err != nil {
		t.Fatal(err)
	}

	go zrpc.Run(zrpc.WithRegisterDiscover(rd), zrpc.WithNodeInfo(node))
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

func TestRunserverWithRegistry3(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("/sayhello", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	node := zrpc.Node{
		ServiceName:     "testdata",
		NodeID:          "33333333-3333-3333-3333-333333333333",
		LocalEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10100},
		ClusterEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10101},
		StateEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10102},
		IsIdle:          true,
	}

	rd, err := newRegisterDiscover(node)
	if err != nil {
		t.Fatal(err)
	}

	go zrpc.Run(zrpc.WithRegisterDiscover(rd), zrpc.WithNodeInfo(node))
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

// getLocalIps 获取本机ip地址（ipv4）
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
