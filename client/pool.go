package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrPoolTimeout timed out waiting to get a client from the client pool.
	ErrPoolTimeout = errors.New("zrpc-cli: connection pool timeout")
)

var timers = sync.Pool{
	New: func() interface{} {
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	},
}

type _cli struct {
	*ZrpcClient
	reuse      int32
	reuseCount int32
	createdAt  time.Time
	usedAt     int64
	waitClose  bool
	breaks     bool
}

func (cli *_cli) UsedAt() time.Time {
	unix := atomic.LoadInt64(&cli.usedAt)
	return time.Unix(unix, 0)
}

func (cli *_cli) SetUsedAt() {
	atomic.AddInt32(&cli.reuseCount, -1)
	atomic.StoreInt64(&cli.usedAt, time.Now().Unix())
}

func (cli *_cli) put() int32 {
	r := atomic.AddInt32(&cli.reuseCount, 1)
	if cli.waitClose && r == cli.reuse {
		cli.Close()
	}
	return r
}

func (cli *_cli) WaitClose() error {
	if atomic.LoadInt32(&cli.reuseCount) == cli.reuse {
		return cli.Close()
	}
	return nil
}

type CliPool struct {
	clis       []*_cli
	idleClis   []*_cli
	poolSize   int
	idleCliLen int
	opt        *options
	serverInfo ServerInfo
	services   sync.Map

	mutex    sync.Mutex
	queue    chan struct{}
	_closed  uint32
	closedCh chan struct{}
}

// NewDirectPool 连接池（只适用于单节点）
func NewDirectPool(server ServerInfo, opt ...Option) (*CliPool, error) {
	defOpts := &options{
		MinIdleConns:       1,
		PoolSize:           3,
		PoolTimeout:        3 * time.Second,
		IdleTimeout:        5 * time.Minute,
		IdleCheckFrequency: time.Minute,
		ReuseCount:         100,
	}
	for _, f := range opt {
		f(defOpts)
	}
	pool := &CliPool{
		clis:       make([]*_cli, 0, defOpts.PoolSize),
		idleClis:   make([]*_cli, 0, defOpts.PoolSize),
		serverInfo: server,
		opt:        defOpts,
		queue:      make(chan struct{}, defOpts.PoolSize*int(defOpts.ReuseCount)),
		closedCh:   make(chan struct{}),
	}

	pool.checkMinIdleCli()
	go func() {
		tick := time.NewTicker(defOpts.IdleCheckFrequency)
		defer tick.Stop()

		for {
			select {
			case <-pool.closedCh:
				return
			case <-tick.C:
				if pool.isClosed() {
					return
				}

				pool.reapStaleClis()
			}
		}
	}()
	return pool, nil
}

func (pool *CliPool) checkMinIdleCli() {
	for pool.poolSize < pool.opt.PoolSize && pool.idleCliLen < pool.opt.MinIdleConns {
		pool.poolSize++
		pool.idleCliLen++
		go func() {
			if _, err := pool.addNewCli(); err != nil {
				pool.mutex.Lock()
				pool.poolSize--
				pool.idleCliLen--
				pool.mutex.Unlock()
			}
		}()
	}
}

func (pool *CliPool) addNewCli() (*_cli, error) {
	cli, err := NewDirectClient(pool.serverInfo)
	if err != nil {
		return nil, err
	}

	time.Sleep(time.Millisecond)
	now := time.Now()
	c := &_cli{
		ZrpcClient: cli,
		reuse:      pool.opt.ReuseCount,
		reuseCount: pool.opt.ReuseCount,
		createdAt:  now,
		usedAt:     now.Unix(),
	}

	pool.clis = append(pool.clis, c)
	pool.idleClis = append(pool.idleClis, c)
	return c, nil
}

func (pool *CliPool) addNewCliWithLock(breaks bool) (*_cli, error) {
	cli, err := NewDirectClient(pool.serverInfo)
	if err != nil {
		return nil, err
	}

	time.Sleep(time.Millisecond)
	now := time.Now()
	c := &_cli{
		ZrpcClient: cli,
		reuse:      pool.opt.ReuseCount,
		reuseCount: pool.opt.ReuseCount,
		createdAt:  now,
		usedAt:     now.Unix(),
	}

	pool.mutex.Lock()
	pool.clis = append(pool.clis, c)
	pool.idleClis = append(pool.idleClis, c)
	if breaks && pool.poolSize >= pool.opt.PoolSize {
		c.breaks = true
	} else {
		pool.poolSize++
		pool.idleCliLen++
	}
	pool.mutex.Unlock()
	return c, nil
}

// Decorator 将 server proxy 装饰为可调用 server
func (pool *CliPool) Decorator(name string, i interface{}, retry int) error {
	if pool.isClosed() {
		return ErrClosed
	}

	_, ok := pool.services.LoadOrStore(name, i)
	if ok {
		return fmt.Errorf("service [%s] is exists", name)
	}

	proxy := newInstanceProxy(name, i, pool)
	err := proxy.init()
	if err != nil {
		pool.services.Delete(name)
		return err
	}

	return nil
}

// GetSerivce 获取已注册 rpc 服务实例以供使用
func (pool *CliPool) GetSerivce(name string) (interface{}, bool) {
	if pool.isClosed() {
		return nil, false
	}
	return pool.services.Load(name)
}

func (pool *CliPool) get(ctx context.Context) (client, error) {
	if pool.isClosed() {
		return nil, ErrClosed
	}

	if err := pool.waitTurn(ctx); err != nil {
		return nil, err
	}

	for {
		pool.mutex.Lock()
		cli := pool.popIdle()
		pool.mutex.Unlock()
		if cli == nil {
			break
		}

		if pool.isStaleCli(cli) {
			pool.mutex.Lock()
			pool.removeCli(cli)
			pool.mutex.Unlock()
			pool.closeCli(cli)
			continue
		}

		return cli, nil
	}

	cli, err := pool.addNewCliWithLock(true)
	if err != nil {
		pool.freeTrun()
		return nil, err
	}

	return cli, nil
}

func (pool *CliPool) waitTurn(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case pool.queue <- struct{}{}:
		return nil
	default:
	}

	timer := timers.Get().(*time.Timer)
	timer.Reset(pool.opt.PoolTimeout)

	select {
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		timers.Put(timer)
		return ctx.Err()
	case pool.queue <- struct{}{}:
		if !timer.Stop() {
			<-timer.C
		}
		timers.Put(timer)
		return nil
	case <-timer.C:
		timers.Put(timer)
		return ErrPoolTimeout
	}
}

func (pool *CliPool) popIdle() *_cli {
	l := len(pool.idleClis)
	if l == 0 {
		return nil
	}

	var cli *_cli
	if pool.opt.PoolFIFO {
		cli = pool.idleClis[0]
		if reuseCount := cli.reuseCount - 1; reuseCount == 0 {
			copy(pool.idleClis, pool.idleClis[1:])
			pool.idleClis = pool.idleClis[:l-1]
			pool.idleCliLen--
			pool.checkMinIdleCli()
		}
	} else {
		i := l - 1
		cli = pool.idleClis[i]
		if reuseCount := cli.reuseCount - 1; reuseCount == 0 {
			pool.idleClis = pool.idleClis[:i]
			pool.idleCliLen--
			pool.checkMinIdleCli()
		}
	}

	return cli
}

func (pool *CliPool) isStaleCli(cli *_cli) bool {
	if pool.opt.IdleTimeout == 0 && pool.opt.MaxConnAge == 0 {
		return false
	}

	now := time.Now()
	if pool.opt.IdleTimeout > 0 && now.Sub(cli.UsedAt()) >= pool.opt.IdleTimeout {
		return true
	}
	if pool.opt.MaxConnAge > 0 && now.Sub(cli.createdAt) >= pool.opt.MaxConnAge {
		return true
	}
	return false
}

func (pool *CliPool) closeCli(cli *_cli) error {
	if pool.opt.OnClose != nil {
		pool.opt.OnClose(cli.ZrpcClient)
	}
	return cli.WaitClose()
}

func (pool *CliPool) removeCli(cli *_cli) {
	for i, c := range pool.clis {
		if c.identity == cli.identity {
			pool.clis[i] = pool.clis[len(pool.clis)-1]
			pool.clis[len(pool.clis)-1] = nil
			pool.clis = pool.clis[:len(pool.clis)-1]
		}
	}
	for i, c := range pool.idleClis {
		if c.identity == cli.identity {
			pool.idleClis = append(pool.idleClis[:i], pool.idleClis[i+1:]...)
		}
	}
	if !cli.breaks {
		pool.poolSize--
		pool.idleCliLen--
	}
	// pool.mutex.Unlock()
}

func (pool *CliPool) put(cli client) {
	defer pool.freeTrun()

	c := cli.(*_cli)
	r := c.put()
	if c.breaks {
		pool.closeCli(c)
		return
	}

	if r == 1 {
		pool.mutex.Lock()
		pool.idleClis = append(pool.idleClis, c)
		pool.mutex.Unlock()
	}
}

func (pool *CliPool) getTrun() {
	pool.queue <- struct{}{}
}

func (pool *CliPool) freeTrun() {
	select {
	case <-pool.queue:
	default:
		return
	}
}

func (pool *CliPool) Len() int {
	pool.mutex.Lock()
	n := pool.poolSize
	pool.mutex.Unlock()
	return n
}

func (pool *CliPool) IdleLen() int {
	pool.mutex.Lock()
	n := pool.idleCliLen
	pool.mutex.Unlock()
	return n
}

func (pool *CliPool) reapStaleClis() int {
	var n int
	for {
		pool.getTrun()

		pool.mutex.Lock()
		cli := pool.reapStaleCli()
		pool.mutex.Unlock()
		pool.freeTrun()
		if cli != nil {
			pool.closeCli(cli)
			n++
		} else {
			break
		}
	}
	return n
}

func (pool *CliPool) reapStaleCli() *_cli {
	idleCliLen := len(pool.idleClis)
	if idleCliLen == 0 {
		return nil
	}

	if idleCliLen > 1 && pool.opt.PoolFIFO {
		cli := pool.idleClis[1]
		if pool.isStaleCli(cli) {
			pool.idleClis = append(pool.idleClis[:1], pool.idleClis[2:]...)
			pool.removeCli(cli)
			pool.idleCliLen--
			return cli
		}
	}

	cli := pool.idleClis[0]
	if !pool.isStaleCli(cli) {
		return nil
	}

	pool.idleClis = append(pool.idleClis[:0], pool.idleClis[1:]...)
	pool.removeCli(cli)
	pool.idleCliLen--
	return cli
}

func (pool *CliPool) isClosed() bool {
	return atomic.LoadUint32(&pool._closed) == 1
}

func (pool *CliPool) Close() error {
	if !atomic.CompareAndSwapUint32(&pool._closed, 0, 1) {
		return ErrClosed
	}
	close(pool.closedCh)

	pool.mutex.Lock()
	for _, cli := range pool.clis {
		cli.Close()
	}

	pool.clis = nil
	pool.idleClis = nil
	pool.idleCliLen = 0
	pool.poolSize = 0
	pool.mutex.Unlock()
	return nil
}
