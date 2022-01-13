package zrpc

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

type socMode int

const (
	frontend socMode = iota + 1 // 前端
	backend                     // 后端
)

type command string

const (
	_CLOSE       = command("close")       // 关闭所有连接
	_CONNECT     = command("connect")     // 新连接
	_DISCONNECT  = command("disconnect")  // 断开到某个断点的连接
	_SUBSCRIBE   = command("subscribe")   // 订阅某topic
	_UNSUBSCRIBE = command("unsubscribe") // 取消订阅
)

type socket struct {
	id          string
	soctype     zmq.Type
	socket      *zmq.Socket
	endpoint    string
	endpoints   map[string]struct{}
	recvChan    chan [][]byte
	sendChan    chan [][]byte
	commandChan chan string
	errChan     chan error

	lock    sync.Mutex
	isClose bool
	mode    socMode
}

func newSocket(id string, t zmq.Type, mode socMode, endpoint string) (*socket, error) {
	soc, err := zmq.NewSocket(t)
	if err != nil {
		return nil, err
	}
	soc.SetIdentity(id)
	if mode == frontend {
		if t != zmq.SUB { // zmq.sub 特别处理，前端表示接收数据，zmq.SUB 是订阅接收不用 Bind
			err := soc.Bind(endpoint)
			if err != nil {
				return nil, err
			}
		}
	}

	if mode == backend {
		if t == zmq.PUB { // zmq.pub 特别处理，使用 PUB 向其他节点推送状态。
			err := soc.Bind(endpoint)
			if err != nil {
				return nil, err
			}
		}
	}

	s := &socket{
		id:          uuid.NewRandom().String(),
		soctype:     t,
		mode:        mode,
		socket:      soc,
		endpoint:    endpoint,
		endpoints:   make(map[string]struct{}),
		recvChan:    make(chan [][]byte, chanCap),
		sendChan:    make(chan [][]byte, chanCap),
		commandChan: make(chan string),
		errChan:     make(chan error),
	}
	go s.mainLoop()
	go s.sendLoop()
	go func() {
		for {
			err := <-s.errChan
			log.Println("zmq err: ", s.endpoint, err)
		}
	}()

	return s, nil
}

func (s *socket) mainLoop() {
	// 用于接收 send 消息
	localPull, err := zmq.NewSocket(zmq.PULL)
	if err != nil {
		s.errChan <- err
		return
	}
	if err := localPull.Connect(fmt.Sprintf("inproc://local_pull_%s", s.id)); err != nil {
		s.errChan <- err
		return
	}
	defer localPull.Close()

	// pipe 用于接收指令
	pipe, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		s.errChan <- err
		return
	}
	if err := pipe.Connect(fmt.Sprintf("inproc://local_pipe_%s", s.id)); err != nil {
		s.errChan <- err
		return
	}
	defer pipe.Close()

	// poller
	poller := zmq.NewPoller()
	poller.Add(s.socket, zmq.POLLIN)
	poller.Add(localPull, zmq.POLLIN)
	poller.Add(pipe, zmq.POLLIN)
	for {
		polls, err := poller.Poll(-1)
		if err != nil {
			s.errChan <- err
			continue
		}

		for _, p := range polls {
			switch soc := p.Socket; soc {
			case pipe:
				cmd, err := pipe.RecvMessage(0)
				if err != nil {
					s.errChan <- err
					return
				}
				switch command(cmd[0]) {
				case _CLOSE: // 关闭
					if s.mode == backend || s.soctype == zmq.SUB {
						for endpoint := range s.endpoints {
							s.socket.Disconnect(endpoint)
						}
					}
					pipe.SendMessage("ok")
					s.socket.Close()
					return
				case _DISCONNECT: // 断开到某个端点的连接
					if s.mode == backend || s.soctype == zmq.SUB {
						endpoint := cmd[1]
						if err := s.socket.Disconnect(endpoint); err != nil {
							s.errChan <- err
						}
					}
					pipe.SendMessage("ok")
				case _CONNECT: // 建立到某个断点的连接
					if s.mode == backend || s.soctype == zmq.SUB {
						endpoint := cmd[1]
						err := s.socket.Connect(endpoint)
						if err != nil {
							s.errChan <- err
						}
						pipe.SendMessage("ok")
					}
				case _SUBSCRIBE: // 订阅消息
					if s.soctype == zmq.SUB {
						topic := cmd[1]
						err := s.socket.SetSubscribe(topic)
						if err != nil {
							s.errChan <- err
						}
						pipe.SendMessage("ok")
					}
				case _UNSUBSCRIBE: // 取消订阅
					if s.soctype == zmq.SUB {
						topic := cmd[1]
						err := s.socket.SetUnsubscribe(topic)
						if err != nil {
							s.errChan <- err
						}
						pipe.SendMessage("ok")
					}
				}
			case localPull:
				msg, err := localPull.RecvMessageBytes(0)
				if err != nil {
					s.errChan <- err
					continue
				}
				_, err = s.socket.SendMessage(msg)
				if err != nil {
					s.errChan <- err
					continue
				}
			case s.socket:
				msg, err := s.socket.RecvMessageBytes(0)
				if err != nil {
					s.errChan <- err
					continue
				}
				s.recvChan <- msg
			}
		}
	}
}

func (s *socket) sendLoop() {
	localPush, err := zmq.NewSocket(zmq.PUSH)
	if err != nil {
		s.errChan <- err
		return
	}
	if err := localPush.Bind(fmt.Sprintf("inproc://local_pull_%s", s.id)); err != nil {
		s.errChan <- err
		return
	}
	defer localPush.Close()

	pipe, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		s.errChan <- err
		return
	}
	if err := pipe.Bind(fmt.Sprintf("inproc://local_pipe_%s", s.id)); err != nil {
		s.errChan <- err
		return
	}
	defer pipe.Close()

	for {
		select {
		case str := <-s.commandChan:
			array := strings.Split(str, " ")
			cmd := array[0]
			switch command(cmd) {
			case _CLOSE:
				_, err := pipe.SendMessage(cmd)
				if err != nil {
					s.errChan <- err
				}
				return
			default:
				_, err := pipe.SendMessage(array)
				if err != nil {
					s.errChan <- err
				}
				_, err = pipe.RecvMessage(0)
				if err != nil {
					s.errChan <- err
				}
			}
		case msg := <-s.sendChan:
			_, err := localPush.SendMessage(msg)
			if err != nil {
				s.errChan <- err
			}
		}
	}
}

func (s *socket) Recv() <-chan [][]byte {
	return s.recvChan
}

func (s *socket) Send() chan<- [][]byte {
	return s.sendChan
}

// Connect 连接到端点 endpoint （仅后端, zmq.SUB除外）
func (s *socket) Connect(endpoint string) {
	if s.mode != backend && s.soctype != zmq.SUB {
		return
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	s.endpoints[endpoint] = struct{}{}
	s.commandChan <- fmt.Sprintf("%s %s", _CONNECT, endpoint)
}

// Disconnect 断开到端点 endpoint 的连接（仅后端）
func (s *socket) Disconnect(endpoint string) {
	if s.mode != backend {
		return
	}
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.endpoints[endpoint]; ok {
		delete(s.endpoints, endpoint)
		s.commandChan <- fmt.Sprintf("%s %s", _DISCONNECT, endpoint)
	}
}

// Subscribe 订阅消息
func (s *socket) Subscribe(topic string) {
	if s.mode != frontend && s.soctype != zmq.SUB {
		return
	}
	s.commandChan <- fmt.Sprintf("%s %s", _SUBSCRIBE, topic)
}

// Unsubscribe 取消订阅
func (s *socket) Unsubscribe(topic string) {
	if s.mode != frontend && s.soctype != zmq.SUB {
		return
	}
	s.commandChan <- fmt.Sprintf("%s %s", _UNSUBSCRIBE, topic)
}

// Close 关闭 socket
func (s *socket) Close() {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.isClose {
		return
	}
	s.commandChan <- string(_CLOSE)
}
