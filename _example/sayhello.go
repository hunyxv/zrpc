package example

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

type Resp struct {
	Name string `msgpack:"name"`
}

type ISayHello interface {
	SayHello(ctx context.Context, name string) (string, error)
	YourName(ctx context.Context) (*Resp, error)
	StreamReq(ctx context.Context, count int, r io.Reader) (bool, error)
	StreamRep(ctx context.Context, count int, w io.WriteCloser) error
	Stream(ctx context.Context, count int, rw io.ReadWriteCloser) error
}

type SayHelloProxy struct {
	SayHello  func(ctx context.Context, name string) (string, error)
	YourName  func(ctx context.Context) (*Resp, error)
	StreamReq func(ctx context.Context, count int, r io.Reader) (bool, error)
	StreamRep func(ctx context.Context, count int, w io.WriteCloser) error
	Stream    func(ctx context.Context, count int, rw io.ReadWriteCloser) error
}

var _ ISayHello = (*SayHello)(nil)

type SayHello struct{}

// SayHello 测试链路追踪
//       ----------------------->
// span1: -->    SayHello    -->
// span2:    --> getIP -->
func (s *SayHello) SayHello(ctx context.Context, name string) (string, error) {
	str := fmt.Sprintf("Hello %s!", name)
	log.Println(str)
	time.Sleep(100 * time.Millisecond)
	getIP(ctx)
	return str, errors.New("a error")
}

// YourName .
func (s *SayHello) YourName(ctx context.Context) (*Resp, error) {
	myName := &Resp{
		Name: "XiaoMing",
	}
	log.Printf("%+v\n", myName)
	return myName, nil
}

// getIP 用于测试 链路追踪
func getIP(ctx context.Context) {
	client := http.DefaultClient
	req, _ := http.NewRequest("GET", "http://icanhazip.com", nil)
	ctx, span := otel.GetTracerProvider().Tracer("SayHello").Start(ctx, "getIP")
	defer span.End()

	req = req.WithContext(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	res, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return
	}
	log.Printf("Received result: %s", body)
}

func (s *SayHello) StreamReq(ctx context.Context, count int, r io.Reader) (bool, error) {
	log.Printf("stream request start ... [count: %d]", count)
	reader := bufio.NewReader(r)
	var i int
	for {
		raw, _, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return false, err
		}
		log.Println(string(raw))
		i++
	}
	log.Println("stream request end...", i)
	return i == count, nil
}

func (s *SayHello) StreamRep(ctx context.Context, count int, w io.WriteCloser) error {
	log.Printf("stream reply start ... [count: %d]", count)
	writer := bufio.NewWriter(w) // 加上写缓存，可以减少连接中传输的数据包数量，以提高吞吐量 （客户端同理）
	defer writer.Flush()         // flush
	for i := 0; i < count; i++ {
		fmt.Fprintf(writer, "line %d\n", i)
		time.Sleep(time.Nanosecond * 100)
	}
	log.Println("stream reply end ...")
	return nil
}

type RequestRespone struct {
	Index int `json:"index"`
}

func (s *SayHello) Stream(ctx context.Context, count int, rw io.ReadWriteCloser) error {
	log.Printf("stream start ... [count: %d]", count)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(rw)
		for i := 0; i < count; i++ {
			data, _, err := reader.ReadLine()
			if err != nil {
				if err == io.EOF {
					break
				}
				log.Println("[error]: ", err)
				break
			}
			var resp RequestRespone
			json.Unmarshal(data, &resp)
			log.Printf("recv: %+v", resp)
		}
	}()

	writer := bufio.NewWriter(rw)
	for i := 0; i < count; i++ {
		req := RequestRespone{
			Index: i,
		}
		log.Printf("send: %v", req)
		raw, _ := json.Marshal(req)
		_, err := writer.Write(raw)
		if err != nil {
			return err
		}

		writer.WriteByte('\n')
		writer.Flush()
	}
	writer.Flush()
	rw.Close()
	wg.Wait()
	log.Println("rw closed")
	log.Println("stream stop ...")
	return nil
}
