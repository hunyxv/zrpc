# zrpc

## 简介

zrpc 是一款简单易用的 RPC 框架。

其支持以下4种请求类型的 RPC 方法：

1. 请求-响应
2. 流式请求
3. 流式响应
4. 双向流式

## zrpc 依赖 [ZeroMQ](https://zeromq.org/) 库

安装 zeromq, 在 [release](https://github.com/zeromq/zeromq4-1/releases) 下载并编译安装：

```shell
tar -zxvf zeromq-4.x.x.tar.gz
cd zeromq-4.x.x
./configure
make && make install 
# 编译后生成的库文件 在目录 /usr/local/lib 下，将其移动到 /usr/lib64 目录
# 或将路径添加到 /etc/ld.so.conf，然后执行 ldconfig 刷新动态链接库。
```

## 使用

> 注意：下面的简单示例为单服务节点，
> 多服务节点需要使用 etcd、zk、consul 等服务发现组件。

接口定义：

```golang
type ISayHello interface {
    SayHello(ctx context.Context, name string) (string, error)
}

type SayHelloProxy struct {
    SayHello func(ctx context.Context, name string) (string, error)
}
```

服务端：
```golang
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/hunyxv/zrpc"
)

var _ ISayHello = (*SayHello)(nil)

type SayHello struct{}

func (s *SayHello) SayHello(ctx context.Context, name string) (string, error) {
    fmt.Println(name)
    return fmt.Sprintf("Hello %s!", name), nil
}

func main() {
    var i *ISayHello
    // 注册服务
    err := zrpc.RegisterServer("sayhello", &SayHello{}, i)
    if err != nil {
        panic(err)
    }

    // 注册多个服务
    // err = zrpc.RegisterServer("sayhello2", &SayHello{}, i)
    // if err != nil {
    //  panic(err)
    // }

    // 启动服务
    go zrpc.Run()
    log.Println("server id: ", zrpc.DefaultNode.NodeID)

    ch := make(chan os.Signal, 1)
    signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
    <-ch
    zrpc.Close()
}

/*
输出：
2022/03/19 13:55:36 register:  sayhello/SayHello 0
2022/03/19 13:55:36 server id:  32a168e5-a749-11ec-9bf9-00163e343ac0
*/
```

客户端调用 RPC 服务：

```golang
package main

import (
    "context"
    "log"
    "time"

    "github.com/hunyxv/zrpc"
    zrpcCli "github.com/hunyxv/zrpc/client"
)

func main() {
    serverinfo := zrpc.DefaultNode
    cli, err := zrpcCli.NewDirectClient(zrpcCli.ServerInfo{
        ServerName:    serverinfo.ServiceName,
        NodeID:        "32a168e5-a749-11ec-9bf9-00163e343ac0",   // 注意节点 id 为上面输出的 server id
        LocalEndpoint: serverinfo.LocalEndpoint,
        StateEndpoint: serverinfo.StateEndpoint,
    })
    if err != nil {
        log.Fatal(err)
    }
    go cli.Run()
    defer cli.Close()

    time.Sleep(100 * time.Millisecond)

    sayHello := new(SayHelloProxy)
    // 装饰一下
    err = cli.Decorator("sayhello", sayHello, 3) // 重试次数为 3 次

    // 调用 RPC 方法
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    resp, err := sayHello.SayHello(ctx, "Hunyxv")
    if err != nil {
        log.Fatal(err)
    }
    log.Println(resp)
}
```

关于“流式请求”、“流式相应”和链路追踪请看 [example](./_example) 


待续...

## 友情链接

- [zmq4](https://github.com/pebbe/zmq4)
- [ants](https://github.com/panjf2000/ants)
