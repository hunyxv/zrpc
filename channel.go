package zrpc

type backendChannel struct {
	channelID string
	soc       *socket
}

// Multiplexer 由其进行分发任务和回复
// 解包， 根据 id 创建channel 或 发送给特定的channel
type Multiplexer interface {
	SendPack(pack *Pack)
	Recv() *Pack
}

type SvcMultiplexer struct {
	activeChannels map[string]*Channel
	broker         Broker
}

func (sm *SvcMultiplexer) SendPack(pack *Pack) error {
	return nil
}

// Channel 表示客户端和服务端一次交互
type Channel struct {
	mux       *Multiplexer
	chanID    string
	zIdentity string
	replyChan chan *Pack
}
