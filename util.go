package zrpc

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path"
	"time"

	"github.com/pborman/uuid"
)

var origin int64

func init() {
	start, err := time.ParseInLocation("2006-01-02 15:04:05", "2021-11-17 11:47:00", time.Local)
	if err != nil {
		panic(err)
	}
	origin = start.UnixNano() / int64(time.Millisecond)
}

func NewMessageID() (id string) {
	now := time.Now().UnixNano()/int64(time.Millisecond) - origin
	_uuid := uuid.NewRandom().Array()
	idPrefix := bytes.NewBuffer([]byte{})
	binary.Write(idPrefix, binary.BigEndian, now)
	var _id [27]byte
	hex.Encode(_id[:], idPrefix.Bytes()[3:])
	_id[10] = '-'
	hex.Encode(_id[11:], _uuid[8:])
	return string(_id[:])
}

type messageFlow struct {
	from   string
	source []string
	strpkg string
	pkg    *Pack
}

func unwrap(msg []string) *messageFlow {
	l := len(msg)
	if l == 2 {
		return &messageFlow{
			from:   msg[0],
			strpkg: msg[1],
		}
	}
	return &messageFlow{
		from:   msg[0],
		source: msg[1 : l-1],
		strpkg: msg[l-1],
	}
}

type rwchannel struct {
	ch        chan []byte
	buf       []byte
	blockSize int
}

func NewRWChannel(size int) io.ReadWriteCloser {
	if size == 0 {
		size = 4096
	}
	return &rwchannel{
		ch:        make(chan []byte),
		blockSize: size,
	}
}

func (rw *rwchannel) Read(b []byte) (n int, err error) {
	if len(rw.buf) > 0 {
		if len(b) <= len(rw.buf) {
			n = copy(b, rw.buf)
			rw.buf = rw.buf[n:]
			return
		}

		n = copy(b, rw.buf)
		rw.buf = rw.buf[:0]
	}

	for {
		data, ok := <-rw.ch
		if !ok {
			return n, io.EOF
		}
		c := copy(b[n:], data)
		n += c
		if c < len(data) {
			rw.buf = data[c:]
			return
		}
	}
}

func (rw *rwchannel) Write(b []byte) (n int, err error) {
	if len(b) <= rw.blockSize {
		rw.ch <- b
		return len(b), nil
	}

	for {
		tmp := make([]byte, rw.blockSize)
		c := copy(tmp, b)
		b = b[c:]
		rw.ch <- tmp[:c]
		n += c
		if c < rw.blockSize {
			return
		}
	}
}

func (rw *rwchannel) Close() error {
	close(rw.ch)
	return nil
}


func getServerName() string {
	pwd, _ := os.Getwd()
	_, name := path.Split(pwd)
	return name
}