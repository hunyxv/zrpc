package testdata

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/hunyxv/zrpc"
	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

type ISayHello interface {
	Hello(ctx context.Context) (string, error)
}

type SayHello struct{}

func (s SayHello) Hello(ctx context.Context) (string, error) {
	log.Println("Hello world!")
	return "world", nil
}

func TestRunserver(t *testing.T) {
	var i *ISayHello
	server := zrpc.NewSvcMultiplexer(zrpc.DefaultNodeState)
	zrpc.RegisterServer("sayhello/", &SayHello{}, i)

	server.Run(context.TODO())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	server.Close()
}

func client(pack *zrpc.Pack) [][]byte {
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(pack.Identity)
	err = soc.Connect("tcp://127.0.0.1:8080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:8080")

	rawPack, err := msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}

	total, err := soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}
	log.Println("total: ", total)

	msg, err := soc.RecvMessageBytes(0)
	if err != nil {
		panic(err)
	}
	log.Println("msg: ", msg, string(msg[0]))
	return msg
}

func TestRunclient(t *testing.T) {
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	pack := &zrpc.Pack{
		Identity:   "test-client",
		Header:     make(zrpc.Header),
		MethodName: "sayhello/Hello",
		Args:       []msgpack.RawMessage{rawCtx},
	}
	r := client(pack)

	var result zrpc.Pack

	err = msgpack.Unmarshal(r[0], &result)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(result)

	var world string
	msgpack.Unmarshal(result.Args[0], &world)
	var e error
	msgpack.Unmarshal(result.Args[1], e)
	t.Log("result: ", world, e)
}
