package zrpc

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/utils/timer"
)

func TestMyMap(t *testing.T) {
	timerWheel, _ := timer.NewHashedWheelTimer(context.Background())
	go timerWheel.Start()
	defer timerWheel.Stop()
	m := newMyMap(timerWheel)
	m.Store("testkey1", "aaaaaa")

	v, ok := m.Load("testkey")
	if !ok {
		t.Fatal("key is not exists")
	}
	t.Log(v)
	time.Sleep(3 * time.Second)
	_, ok = m.Load("testkey")
	if !ok {
		t.Fatal("key is not exists")
	}
	time.Sleep(5 * time.Second)
	_, ok = m.Load("testkey")
	if ok {
		t.Fatal("key is not deleted")
	}
}

func TestMessageID(t *testing.T) {
	now := time.Now()
	id := NewMessageID()
	cost := time.Since(now)
	t.Log(id, cost)
}

func BenchmarkMessageID(b *testing.B) {
	var m sync.Map

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			id := NewMessageID()
			if _, loaded := m.LoadOrStore(id, struct{}{}); loaded {
				b.Fatal()
			}
		}
	})
}

func TestRWChanel(t *testing.T) {
	rw := newRWChannel(0)

	buf := bufio.NewReadWriter(bufio.NewReader(rw), bufio.NewWriter(rw))
	go func() {
		sum := 0
		f, err := os.Open("./nodestate_test.go")
		if err != nil {
			panic(err)
		}
		defer f.Close()

		for {
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
			buf.Flush()
		}
		t.Logf("写入总数： %d byte", sum)
		t.Log("----------------------------------------")
	}()

	go func() {
		sum := 0
		var data = make([]byte, 512)
		f, err := os.OpenFile("./nodestate_test.go.back", syscall.O_CREAT|syscall.O_RDWR, 0666)
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

func TestRWChanel2(t *testing.T) {
	rw := newRWChannel(0)

	buf := bufio.NewReadWriter(bufio.NewReader(rw), bufio.NewWriter(rw))

	str := "hello world!"
	buf.Write([]byte(str))
	buf.Flush()

	b := make([]byte, 20)
	n, err := buf.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("-> ", b[:n])
	time.Sleep(3 * time.Second)
	rw.Close()
	time.Sleep(time.Second)
}
