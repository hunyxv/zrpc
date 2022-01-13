package testdata

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

type ISayHello interface {
	Hello(ctx context.Context) (string, error)
	StreamReqFunc(ctx context.Context, reader io.Reader) (string, error)
}

var _ ISayHello = (*SayHello)(nil)

type SayHello struct{}

func (s SayHello) Hello(ctx context.Context) (string, error) {
	log.Println("Hello world!")
	return "world", errors.New("a error")
}

func (s SayHello) StreamReqFunc(ctx context.Context, reader io.Reader) (string, error) {
	log.Println("stream req func...")
	buf := bufio.NewReader(reader)
	for {
		line, _, err := buf.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "error", err
		}
		log.Println("line: ", string(line))
	}
	return "stream request", nil
}

func TestRunserver(t *testing.T) {
	var i *ISayHello
	server := zrpc.NewSvcMultiplexer(zrpc.DefaultNodeState)
	err := zrpc.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	server.Run(context.TODO())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	server.Close()
}

func client(soc *zmq.Socket, pack *zrpc.Pack) [][]byte {

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
	id := "test-client"
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:8080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:8080")

	now := time.Now()
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	pack := &zrpc.Pack{
		Identity:   id,
		Header:     make(zrpc.Header),
		MethodName: "sayhello/Hello",
		Args:       []msgpack.RawMessage{rawCtx},
	}
	r := client(soc, pack)

	var result zrpc.Pack

	err = msgpack.Unmarshal(r[0], &result)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(result)

	var world string
	msgpack.Unmarshal(result.Args[0], &world)
	var e string
	msgpack.Unmarshal(result.Args[1], &e)
	t.Log("result: ", world, errors.New(e))
	t.Logf("takes %s", time.Since(now))
}

func TestStreamReqFunc(t *testing.T) {
	id := "test-client"
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:8080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:8080")

	now := time.Now()
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	rawline, _ := msgpack.Marshal([]byte(fmt.Sprintf("hello %d\n", 0)))
	pack := &zrpc.Pack{
		Identity:   id,
		Header:     make(zrpc.Header),
		MethodName: "sayhello/StreamReqFunc",
		Args:       []msgpack.RawMessage{rawCtx, rawline},
	}
	for i := 0; i < 10; i++ {
		rawPack, err := msgpack.Marshal(&pack)
		if err != nil {
			panic(err)
		}
		t.Log(string(rawline))
		total, err := soc.SendMessage(rawPack)
		if err != nil {
			panic(err)
		}
		log.Println("total: ", total)
		rawline, _ = msgpack.Marshal([]byte(fmt.Sprintf("hello %d\n", i+1)))
		pack = &zrpc.Pack{
			Identity:   id,
			Header:     make(zrpc.Header),
			MethodName: "sayhello/StreamReqFunc",
			Args:       []msgpack.RawMessage{rawline},
		}
	}

	// end
	pack = &zrpc.Pack{
		Identity:   id,
		MethodName: zrpc.STREAM_END,
	}
	rawPack, err := msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}
	total, err := soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}
	log.Println("end total: ", total)

	msg, err := soc.RecvMessageBytes(0)
	if err != nil {
		panic(err)
	}
	log.Println("msg: ", msg, string(msg[0]))
	t.Logf("takes %s", time.Since(now))
}
