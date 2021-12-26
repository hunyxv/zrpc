package zrpc


var (
	DefaultRPCInstance = newRPCInstance()
)

func RegisterServer(name string, server interface{}, conventions interface{}) error {
	return DefaultRPCInstance.RegisterServer(name, server, conventions)
}

