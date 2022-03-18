# SayHello

栗子🌰，包含链路追踪的使用。

启动服务端：
```
go run server/main.go
```

测试 rpc 请求：
```
go run client/main.go --fname=SayHello 
go run client/main.go --fname=YourName
go run client/main.go --fname=StreamReq
```