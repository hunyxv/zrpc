package testdata

import (
	"bufio"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
)

func TestMessageID(t *testing.T) {
	now := time.Now()
	id := zrpc.NewMessageID()
	cost := time.Since(now)
	t.Log(id, cost)
}

func BenchmarkMessageID(b *testing.B) {
	var m sync.Map

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			id := zrpc.NewMessageID()
			if _, loaded := m.LoadOrStore(id, struct{}{}); loaded {
				b.Fatal()
			}
		}
	})
}

func TestRWChanel(t *testing.T) {
	rw := zrpc.NewRWChannel(100)

	buf := bufio.NewReadWriter(bufio.NewReader(rw), bufio.NewWriter(rw))
	go func() {
		sum := 0
		f, err := os.Open("./111.jpg")
		if err != nil {
			panic(err)
		}
		defer f.Close()

		for  {
			data := make([]byte, 1000)
			n, err := f.Read(data)
			if err != nil {
				if err == io.EOF {
					break
				}
			}
			//data := make([]byte, 1024)
			n, err = buf.Write(data[:n])
			if err != nil {
				panic(err)
			}
			sum += n
		}
		t.Logf("写入总数： %d byte", sum)
		t.Log("----------------------------------------")
		buf.Flush()
	}()

	go func() {
		sum := 0
		var data = make([]byte, 512)
		f, err := os.OpenFile("./222.jpg", syscall.O_CREAT|syscall.O_RDWR, 0666)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		for {
			n, err := buf.Read(data)
			if err != nil {
				if err == io.EOF {
					break
				}
			}
			sum += n
			f.Write(data[:n])
		}
		t.Logf("读出总数：%d byte", sum)
	}()

	time.Sleep(3 * time.Second)
	rw.Close()
	time.Sleep(time.Second)
}
