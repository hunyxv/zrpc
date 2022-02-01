package testdata

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/sirupsen/logrus"
)

func runBroker(state *zrpc.NodeState) (zrpc.Broker, error) {
	broker, err := zrpc.NewBroker(state, 5*time.Second, logrus.StandardLogger())
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
			LocalEndpoint:   "tcp://127.0.0.1:8080",
			ClusterEndpoint: "tcp://127.0.0.1:8081",
			StateEndpoint:   "tcp://127.0.0.1:8082",
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
			LocalEndpoint:   "tcp://127.0.0.1:9090",
			ClusterEndpoint: "tcp://127.0.0.1:9091",
			StateEndpoint:   "tcp://127.0.0.1:9092",
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
