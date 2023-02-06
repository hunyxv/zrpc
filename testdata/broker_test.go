package testdata

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
)

type logger struct{}

func (*logger) Debug(args ...any) {
	log.Println(args...)
}
func (*logger) Debugf(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Info(args ...any) {
	log.Println(args...)
}
func (*logger) Infof(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Warn(args ...any) {
	log.Println(args...)
}
func (*logger) Warnf(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Error(args ...any) {
	log.Println(args...)
}
func (*logger) Errorf(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Fatal(args ...any) {
	log.Fatal(args...)
}

func runBroker(state *zrpc.NodeState) (zrpc.Broker, error) {
	broker, err := zrpc.NewBroker(state, 5*time.Second, &logger{})
	if err != nil {
		return nil, err
	}
	go broker.Run()
	return broker, nil
}

func TestBroker(t *testing.T) {
	state1 := &zrpc.NodeState{
		Node: &zrpc.Node{
			ServiceName:     "test",
			NodeID:          "test-1",
			LocalEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10080},
			ClusterEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10081},
			StateEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10082},
		},
	}

	broker1, err := runBroker(state1)
	if err != nil {
		t.Fatal(err)
	}

	state2 := &zrpc.NodeState{
		Node: &zrpc.Node{
			ServiceName:     "test",
			NodeID:          "test-2",
			LocalEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10090},
			ClusterEndpoint: zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10091},
			StateEndpoint:   zrpc.Endpoint{Scheme: "tcp", Host: "0.0.0.0", Port: 10092},
		},
	}

	broker2, err := runBroker(state2)
	if err != nil {
		t.Fatal(err)
	}

	broker1.AddPeerNode(state2.Node)
	broker2.AddPeerNode(state1.Node)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	broker1.Close(nil)
	broker2.Close(nil)
}
