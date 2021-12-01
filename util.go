package zrpc

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
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
