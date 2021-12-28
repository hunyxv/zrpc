package zrpc

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
	"github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

/*
	endpoint 可以是 scheme://[ip]:port，也可以是 scheme://[demain]:port

*/

const (
	REQUEST    = string(rune(iota + 1)) // 请求包
	REPLY                               // 响应包，与 req 对应的 回复
	HEARTBEAT                           // 心跳包
	SYNCSTATE                           // 同步节点状态
	DISCONNECT                          // 通知让客户端断开连接
	ERROR                               // 异常包
	STREAM                              // stream
	STREAM_END
)

var (
	METHOD_NAME = map[string]string{
		REQUEST:    "REQUEST",
		REPLY:      "REPLY",
		HEARTBEAT:  "HEARTBEAT",
		DISCONNECT: "DISCONNECT",
		ERROR:      "ERROR",
	}
)

type mode int

const (
	Singleton mode = iota // 单节点模式
	Cloud                 // 云模式/集群模式
)

var chanCap = func() int {
	if runtime.GOMAXPROCS(0) == 1 {
		return 0
	}
	return 1
}()

// Broker 代理
type Broker interface {
	// AddPeerNode 添加平行节点
	AddPeerNode(node *Node)
	// DelPeerNode 删除平行节点
	DelPeerNode(node *Node)
	// AllPeerNode 获取所有平行节点 endpoint
	AllPeerNode() []Node
	// ForwardToPeerNode 本节点处理不了了，转发给其他节点
	ForwardToPeerNode(to string, pack *Pack)
	// NewTask 获得新任务
	NewTask() <-chan *Pack
	// Reply 回复结果
	Reply(p *Pack) error
	// SetBrokerMode 设置 broker 运行模式
	SetBrokerMode(m mode)
	// PublishNodeState 发布本节点状态
	PublishNodeState()
	// Run
	Run()
	// Close 关闭
	Close(clis []string)
}

// 平行节点管理
//  1. 建立到平行节点的连接：1>. 请求接发连接； 2>. 状态同步连接
//  2. 自动断开（ping）超时的
//	3. 保存各个平行节点的状态
//  4. 当当前节点忙时，向空闲节点转发 task
//  5. 接收其他节点的回复/响应，然后交给 broker，发送给客户端

var _ Broker = (*broker)(nil)

type broker struct {
	*peerNodeManager

	ctx      context.Context
	cancel   context.CancelFunc
	taskChan chan *Pack
	localfe  *socket // local frontend
	state    *NodeState
}

func NewBroker(state *NodeState, hbInterval time.Duration) (Broker, error) {
	ctx, cancel := context.WithCancel(context.Background())
	manager, err := newPeerNodeManager(state, hbInterval)
	if err != nil {
		cancel()
		return nil, err
	}

	localfe, err := newSocket(state.NodeID, zmq.ROUTER, frontend, state.LocalEndpoint)
	if err != nil {
		manager.Close()
		cancel()
		return nil, err
	}

	return &broker{
		peerNodeManager: manager,

		ctx:      ctx,
		cancel:   cancel,
		taskChan: make(chan *Pack, chanCap),
		localfe:  localfe,
		state:    state,
	}, nil
}

func (b *broker) NewTask() <-chan *Pack {
	return b.taskChan
}

// Reply 回复执行结果
func (b *broker) Reply(p *Pack) error {
	lastNode := p.Header.Pop(PACKPATH)
	if lastNode == "" {
		to := p.Identity
		p.Identity = b.state.NodeID
		raw, _ := msgpack.Marshal(p)
		b.localfe.Send() <- [][]byte{[]byte(to), raw}
	} else { // 应该回复给平行节点
		raw, _ := msgpack.Marshal(p)
		b.clusterfe.Send() <- [][]byte{[]byte(lastNode), raw}
	}
	return nil
}

func (b *broker) SetBrokerMode(m mode) {
	// TODO
}

// Run 开始接收来自客户端的 pack，并定时向平行节点发送心跳包
//  从 cluster 后端接收回复，然后将其转发到客户端或请求来源节点
func (b *broker) Run() {
	tick := time.NewTicker(b.hbInterval)
	defer tick.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-tick.C: // 发送心跳包
			heartbeat := &Pack{
				Identity:   b.NodeState.NodeID,
				MethodName: HEARTBEAT,
			}
			raw, _ := msgpack.Marshal(heartbeat)
			b.statebe.sendChan <- [][]byte{raw}
			b.lock.Lock()
			for _, nodeState := range b.peerState {
				// 超时了断开到该节点的连接
				// 如果后面还收到了该节点转发的请求，那么重新连接即可
				if time.Until(nodeState.expiration) < 0 {
					b.DelPeerNode(nodeState.Node)
				}
			}
			b.lock.Unlock()
		case raws := <-b.statefe.Recv(): // 订阅的消息
			var pack *Pack
			msgpack.Unmarshal(raws[0], &pack)
			msgfrom := pack.Identity
			switch pack.MethodName {
			case HEARTBEAT: // 收到的心跳包，更新节点的过期时间
				b.lock.Lock()
				if state, ok := b.peerState[msgfrom]; ok {
					state.expiration = time.Now().Add(b.hbInterval * 3)
					logger.WithFields(logrus.Fields{"from": msgfrom, "expiration": state.expiration}).Debug("heartbeat")
				}
				b.lock.Unlock()
			case SYNCSTATE:
				var node *Node
				if err := msgpack.Unmarshal(pack.Args[0], &node); err != nil {
					logger.WithError(err).WithFields(logrus.Fields{
						"pack": pack, "from": msgfrom}).Error("msgpack unmarshal err")
					continue
				}
				b.lock.Lock()
				if state, ok := b.peerState[msgfrom]; ok {
					state.expiration = time.Now().Add(b.hbInterval * 3)
					b.refreshNodeStatus(node)
					b.lock.Unlock()
				} else {
					b.lock.Unlock()
					b.AddPeerNode(node)
				}
			}
		case raws := <-b.localfe.Recv(): // 本地客户端的请求
			msgfrom := string(raws[0])
			var pack *Pack
			msgpack.Unmarshal(raws[1], &pack)
			if pack.Identity != msgfrom {
				logger.Warnf("pack.id: %s, from: %s", pack.Identity, msgfrom)
				continue
			}
			// 来自客户端的心跳交由上层处理
			b.taskChan <- pack
		case raws := <-b.clusterfe.Recv(): // 来自其他节点转发过来的数据
			// msgfrom := string(raws[0])
			var pack *Pack
			msgpack.Unmarshal(raws[1], &pack)
			// ttlStr := pack.Get(TTL)
			// ttl, _ := strconv.Atoi(ttlStr) // 跳数超没超过限制上层来决定
			// pack.Header.Set(TTL, strconv.Itoa(ttl))
			// pack.Header.Add(PACKPATH, msgfrom)
			b.taskChan <- pack // 跳数超没超过限制上层来决定
		case raws := <-b.clusterbe.Recv(): // 开始接收后端数据(返回的结果)
			var pack *Pack
			msgpack.Unmarshal(raws[1], &pack)
			lastNode := pack.Header.Pop(PACKPATH)
			if lastNode == "" {
				// 那就是应该发给客户端
				b.localfe.Send() <- [][]byte{[]byte(pack.Identity), raws[1]}
			} else {
				b.clusterfe.Send() <- [][]byte{[]byte(lastNode), raws[1]}
			}
		}
	}
}

func (b *broker) Close(clis []string) {
	disconnectPack := &Pack{
		Identity:   b.state.NodeID,
		MethodName: DISCONNECT,
	}
	raw, _ := msgpack.Marshal(disconnectPack)
	for _, to := range clis {
		b.localfe.Send() <- [][]byte{[]byte(to), raw}
	}

	b.cancel()
	b.localfe.Close()
	b.peerNodeManager.Close()
}

type peerNodeManager struct {
	NodeState  *NodeState            // 本节点标识
	hbInterval time.Duration         // 心跳间隔
	peerState  map[string]*NodeState // nodeid:NodeState
	lock       sync.RWMutex

	clusterfe *socket
	clusterbe *socket
	statefe   *socket
	statebe   *socket
}

func newPeerNodeManager(state *NodeState, hbInterval time.Duration) (*peerNodeManager, error) {
	clusterfe, err := newSocket(state.NodeID, zmq.ROUTER, frontend, state.ClusterEndpoint)
	if err != nil {
		return nil, err
	}

	clusterbe, err := newSocket(state.NodeID, zmq.ROUTER, backend, "")
	if err != nil {
		clusterbe.Close()
		return nil, err
	}

	statefe, err := newSocket(state.NodeID, zmq.SUB, frontend, "")
	if err != nil {
		clusterfe.Close()
		clusterbe.Close()
		return nil, err
	}
	statefe.Subscribe("")

	statebe, err := newSocket(state.NodeID, zmq.PUB, backend, state.StateEndpoint)
	if err != nil {
		clusterfe.Close()
		clusterbe.Close()
		statefe.Close()
		return nil, err
	}

	manager := &peerNodeManager{
		NodeState:  state,
		hbInterval: hbInterval,
		peerState:  make(map[string]*NodeState),
		// peerList:   make([]string, 0),

		clusterfe: clusterfe,
		clusterbe: clusterbe,
		statefe:   statefe,
		statebe:   statebe,
	}

	return manager, nil
}

// AddPeerNode 添加平行节点
func (peer *peerNodeManager) AddPeerNode(node *Node) {
	peer.lock.Lock()
	defer peer.lock.Unlock()
	if _, ok := peer.peerState[node.NodeID]; !ok {
		state := &NodeState{Node: node}
		state.expiration = time.Now().Add(peer.hbInterval * 3)
		peer.peerState[node.NodeID] = state
		peer.clusterbe.Connect(node.ClusterEndpoint) // 连接到这个节点
		peer.statefe.Connect(node.StateEndpoint)     // 订阅该节点状态
	}
}

// DelPeerNode 删除到平行节点的连接
func (peer *peerNodeManager) DelPeerNode(node *Node) {
	peer.lock.Lock()
	defer peer.lock.Unlock()
	if _, ok := peer.peerState[node.NodeID]; ok {
		delete(peer.peerState, node.NodeID)
		peer.clusterbe.Disconnect(node.ClusterEndpoint)
		peer.statefe.Disconnect(node.StateEndpoint)
	}
}

func (peer *peerNodeManager) AllPeerNode() []Node {
	peer.lock.Lock()
	defer peer.lock.Unlock()
	nodes := make([]Node, len(peer.peerState))
	for _, state := range peer.peerState {
		node := *state.Node
		nodes = append(nodes, node)
	}
	return nodes
}

// ForwardToPeerNode 本节点处理不了了，转发给其他节点
func (peer *peerNodeManager) ForwardToPeerNode(to string, pack *Pack) {
	ttlStr := pack.Get(TTL)
	ttl, _ := strconv.Atoi(ttlStr)
	pack.Header.Set(TTL, strconv.Itoa(ttl+1)) // 跳数+1
	pack.Header.Add(PACKPATH, peer.NodeState.NodeID)

	b, _ := msgpack.Marshal(pack)
	peer.clusterbe.Send() <- [][]byte{[]byte(to), b}
}

// PublishNodeState 发布本节点状态
func (peer *peerNodeManager) PublishNodeState() {
	pack := &Pack{
		Identity:   peer.NodeState.NodeID,
		MethodName: SYNCSTATE,
	}
	// 空闲&&暂停状态
	if peer.NodeState.isIdle() && peer.NodeState.isPausing() {
		peer.NodeState.pursue()
		state := peer.NodeState.Marshal()
		pack.Args = append(pack.Args, state)
		bytePack, _ := msgpack.Marshal(pack)
		peer.statebe.Send() <- [][]byte{bytePack}
		return
	}
	// 繁忙&&却非暂停状态
	if !peer.NodeState.isIdle() && !peer.NodeState.isPausing() {
		peer.NodeState.pause()
		state := peer.NodeState.Marshal()
		pack.Args = append(pack.Args, state)
		bytePack, _ := msgpack.Marshal(pack)
		peer.statebe.Send() <- [][]byte{bytePack}
	}
}

// refreshNodeStatus 接收订阅的节点状态，刷新节点信息（主要是空闲繁忙状态）
func (peer *peerNodeManager) refreshNodeStatus(node *Node) {
	peer.lock.Lock()
	peer.peerState[node.NodeID].Node = node
	peer.lock.Unlock()
}

func (peer *peerNodeManager) Close() error {
	peer.clusterfe.Close()
	peer.clusterbe.Close()
	peer.statefe.Close()
	peer.statebe.Close()
	return nil
}
