package testdata

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
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
	StreamRespFunc(ctx context.Context, num int, writer io.WriteCloser) error
	StreamFunc(ctx context.Context, total int, rw io.ReadWriteCloser) error
}

var _ ISayHello = (*SayHello)(nil)

type SayHello struct{}

func (s *SayHello) Hello(ctx context.Context) (string, error) {
	log.Println("Hello world!")
	return "world", errors.New("a error")
}

func (s *SayHello) StreamReqFunc(ctx context.Context, reader io.Reader) (string, error) {
	log.Println("stream req func...")
	buf := bufio.NewReader(reader)
	// file, _ := os.OpenFile("file.jpeg", os.O_WRONLY|os.O_CREATE, 0666)
	// defer file.Close()
	for {
		line, _, err := buf.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "error", err
		}
		log.Println("line: ", string(line))
		time.Sleep(100 * time.Millisecond)
		//file.Write(line)
	}
	log.Println("end stream req func")
	return "stream request end", nil
}

func (s *SayHello) StreamRespFunc(ctx context.Context, num int, writer io.WriteCloser) error {
	log.Println("stream func ... ", num)
	for i := 0; i < num; i++ {
		n, err := writer.Write([]byte(fmt.Sprintf("Response %d\n", i)))
		if err != nil {
			log.Printf("StreamRespFunc: %v", err)
			return err
		}
		log.Printf("[%d] send %d byte", i, n)
	}
	log.Println("end stream resp func")
	writer.Close()
	return nil
}

type StreamReq struct {
	Index int    `json:"index"`
	Data  string `json:"data"`
}

type StreamResp struct {
	Index int `json:"index"`
}

func (s *SayHello) StreamFunc(ctx context.Context, total int, rw io.ReadWriteCloser) error {
	log.Println("stream func ... ")
	c := make(chan int)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(rw)
		for {
			data, _, err := reader.ReadLine()
			if err != nil {
				if err == io.EOF {
					log.Println("StreamFunc reader EOF")
					return
				}
				log.Println("StreamFunc err: ", err)
				return
			}
			var req *StreamReq
			if err := json.Unmarshal(data, &req); err != nil {
				log.Println("StreamFunc err: ", err)
				return
			}
			log.Printf("req [%d]: %s", req.Index, req.Data)
			c <- req.Index
		}
	}()

	for i := 0; i < total; i += 5 {
		var j int
		for ; j < 5 && j < total-i; j++ {
			resp := &StreamResp{
				Index: i + j,
			}
			log.Println("send ", i+j)
			data, err := json.Marshal(resp)
			if err != nil {
				log.Println("StreamFunc err: ", err)
				return err
			}
			data = append(data, '\r', '\n')
			rw.Write(data)
		}

		for j != 0 {
			<-c
			j--
		}
	}
	rw.Close()
	wg.Wait()
	log.Println("end stream func")
	return nil
}

// 空闲服务
func TestRunserverIdle(t *testing.T) {
	var i *ISayHello
	err := zrpc.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}
	// 为了测试，节点 id 设置为 111...
	zrpc.DefaultNode.NodeID = "11111111-1111-1111-1111-111111111111"

	go zrpc.Run()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	zrpc.Close()
}

// 满载服务
func TestRunserverBusy(t *testing.T) {
	rpcInstance := zrpc.NewRPCInstance()
	var i *ISayHello
	server := zrpc.NewSvcMultiplexer(rpcInstance, zrpc.WithLogger(&logger{}), zrpc.WithNodeInfo(zrpc.Node{
		ServiceName:     "222222",
		NodeID:          "22222222-2222-2222-2222-222222222222", // 为了测试，节点 id 设置为 222...
		LocalEndpoint:   "tcp://0.0.0.0:9080",
		ClusterEndpoint: "tcp://0.0.0.0:9081",
		StateEndpoint:   "tcp://0.0.0.0:9082",
		IsIdle:          false, // 表示本节点已经满载了
	}))
	zrpc.DefaultNode.NodeID = "11111111-1111-1111-1111-111111111111"
	server.AddPeerNode(&zrpc.DefaultNode) // 添加上面那个空闲节点
	t.Log(zrpc.DefaultNode.ServiceName)
	err := rpcInstance.RegisterServer("sayhello/", &SayHello{}, i)
	if err != nil {
		t.Fatal(err)
	}

	go server.Run()
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
	//log.Println("msg: ", msg, string(msg[0]))
	return msg
}

func TestReqRepFunc(t *testing.T) {
	id := "test-client" + fmt.Sprintf("%d", time.Now().UnixNano())
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:9080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:9080")

	err = soc.Connect("tcp://127.0.0.1:8080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:8080")

	for i := 0; i < 10; i++ {

		now := time.Now()
		ctx := &zrpc.Context{
			Context: context.Background(),
		}
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
		r := client(soc, pack)

		var result zrpc.Pack

		err = msgpack.Unmarshal(r[0], &result)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(result)

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
}

func TestStreamReqFunc(t *testing.T) {
	id := "test-client" + fmt.Sprintf("%d", time.Now().UnixNano())
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:9080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:9080")

	now := time.Now()
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	rawline := []byte(fmt.Sprintf("hello %d\n", 0))
	pack := &zrpc.Pack{
		Identity: id,
		Header:   make(zrpc.Header),
		Stage:    zrpc.REQUEST,
		Args:     [][]byte{rawCtx, rawline},
	}
	pack.SetMethodName("sayhello/StreamReqFunc")
	msgid := zrpc.NewMessageID()
	pack.Set(zrpc.MESSAGEID, msgid)
	for i := 0; i < 100; i++ {
		rawPack, err := msgpack.Marshal(&pack)
		if err != nil {
			panic(err)
		}
		//t.Log(string(rawline))
		_, err = soc.SendMessage(rawPack)
		if err != nil {
			panic(err)
		}
		//log.Println("total: ", total)
		rawline = []byte(fmt.Sprintf("hello %d\n", i+1))
		pack = &zrpc.Pack{
			Identity: id,
			Header:   make(zrpc.Header),
			Stage:    zrpc.STREAM,
			Args:     [][]byte{rawline},
		}
		pack.SetMethodName("sayhello/StreamReqFunc")
		pack.Set(zrpc.MESSAGEID, msgid)
	}

	// end
	pack = &zrpc.Pack{
		Identity: id,
		Stage:    zrpc.STREAM_END,
	}
	pack.SetMethodName("sayhello/StreamReqFunc")
	pack.Set(zrpc.MESSAGEID, msgid)
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
	time.Sleep(time.Second)
}

func TestStreamRespFunc(t *testing.T) {
	id := "test-client" + fmt.Sprintf("%d", time.Now().UnixNano())
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:9080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:9080")

	now := time.Now()
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	rawline, err := msgpack.Marshal(6000)
	if err != nil {
		t.Fatal(err)
	}
	pack := &zrpc.Pack{
		Identity: id,
		Header:   make(zrpc.Header),
		Stage:    zrpc.REQUEST,
		Args:     [][]byte{rawCtx, rawline, {}},
	}
	pack.SetMethodName("sayhello/StreamRespFunc")
	msgid := zrpc.NewMessageID()
	pack.Set(zrpc.MESSAGEID, msgid)

	// 序列化
	rawPack, err := msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}
	// 发送请求
	_, err = soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}

	for {
		msg, err := soc.RecvMessageBytes(0)
		if err != nil {
			t.Fatal(err)
		}

		var data *zrpc.Pack
		if err := msgpack.Unmarshal(msg[0], &data); err != nil {
			t.Fatal(err)
		}
		if data.MethodName() == zrpc.ERROR || data.Stage == zrpc.ERROR {
			t.Fatal(string(data.Args[0]))
		}
		if data.Stage == zrpc.STREAM_END {
			t.Log("stream end...")
			break
		}

		t.Log("stream resp: ", string(data.Args[0]))
	}
	msg, err := soc.RecvMessageBytes(0)
	if err != nil {
		t.Fatal(err)
	}
	var data *zrpc.Pack
	if err := msgpack.Unmarshal(msg[0], &data); err != nil {
		t.Fatal(err)
	}
	if data.Stage != zrpc.REPLY {
		t.Log(data.Stage == zrpc.STREAM_END)
		t.Fatal("unknow err")
	}
	t.Log(time.Since(now))
}

func TestStreamFunc(t *testing.T) {
	id := "test-client" + fmt.Sprintf("%d", time.Now().UnixNano())
	soc, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	soc.SetIdentity(id)
	err = soc.Connect("tcp://127.0.0.1:9080")
	if err != nil {
		panic(err)
	}
	defer soc.Close()
	defer soc.Disconnect("tcp://127.0.0.1:9080")

	now := time.Now()
	ctx := &zrpc.Context{
		Context: context.Background(),
	}
	rawCtx, err := msgpack.Marshal(ctx)
	if err != nil {
		panic(err)
	}

	number := 1000
	rawline, err := msgpack.Marshal(number)
	if err != nil {
		t.Fatal(err)
	}
	pack := &zrpc.Pack{
		Identity: id,
		Header:   make(zrpc.Header),
		Stage:    zrpc.REQUEST,
		Args:     [][]byte{rawCtx, rawline, {}},
	}
	pack.SetMethodName("sayhello/StreamFunc")
	msgid := zrpc.NewMessageID()
	t.Log("-------------->>> ", msgid)
	pack.Set(zrpc.MESSAGEID, msgid)

	// 序列化
	rawPack, err := msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}
	// 发送请求
	_, err = soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}

	r, w := io.Pipe()
	go func() {
		for {
			msg, err := soc.RecvMessageBytes(0)
			if err != nil {
				log.Fatal(err)
			}

			var data *zrpc.Pack
			if err := msgpack.Unmarshal(msg[0], &data); err != nil {
				log.Fatal(err)
			}
			if data.MethodName() == zrpc.ERROR || data.Stage == zrpc.ERROR {
				log.Fatal(string(data.Args[0]))
			}
			if data.Stage == zrpc.STREAM_END {
				log.Println("stream end...")
				break
			}
			w.Write(data.Args[0])
		}
		w.Close()
	}()
	//reader := bufio.NewReader(r)
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		data := scanner.Bytes()
		//log.Println(data)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}

		var resp *StreamResp
		err = json.Unmarshal(data, &resp)
		if err != nil {
			t.Fatal(err)
		}
		log.Printf("%+v", resp)
		req := &StreamReq{
			Index: resp.Index,
			Data:  fmt.Sprintf("stream [%d]", resp.Index),
		}
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, '\r', '\n')
		reqPack := &zrpc.Pack{
			Identity: id,
			Header:   make(zrpc.Header),
			Stage:    zrpc.STREAM,
			Args:     [][]byte{b},
		}
		reqPack.SetMethodName("sayhello/StreamFunc")
		reqPack.Set(zrpc.MESSAGEID, msgid)
		rawPack, err := msgpack.Marshal(reqPack)
		if err != nil {
			panic(err)
		}

		_, err = soc.SendMessage(rawPack)
		if err != nil {
			panic(err)
		}
	}

	// end
	pack = &zrpc.Pack{
		Identity: id,
		Stage:    zrpc.STREAM_END,
	}
	pack.SetMethodName("sayhello/StreamFunc")
	pack.Set(zrpc.MESSAGEID, msgid)
	rawPack, err = msgpack.Marshal(&pack)
	if err != nil {
		panic(err)
	}
	_, err = soc.SendMessage(rawPack)
	if err != nil {
		panic(err)
	}
	t.Log(time.Since(now))
	time.Sleep(100 * time.Millisecond)
}
