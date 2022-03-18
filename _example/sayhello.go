package example

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
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
}

type SayHelloProxy struct {
	SayHello  func(ctx context.Context, name string) (string, error)
	YourName  func(ctx context.Context) (*Resp, error)
	StreamReq func(ctx context.Context, count int, r io.Reader) (bool, error)
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
	log.Printf("stream req start ... [count: %d]", count)
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
	log.Println("stream req end...", i)
	return i == count, nil
}
