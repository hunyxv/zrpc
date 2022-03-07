# zrpc
centos 安装 zeromq 在 [release](https://github.com/zeromq/zeromq4-1/releases) 下载压缩包：
```shell
tar -zxvf zeromq-4.1.8.tar.gz
cd zeromq-4.1.8
./configure
make && make install # 编译后生成的库文件 在目录 /usr/local/lib 下，将其移动到 /usr/lib64 目录，或将路径添加到 /etc/ld.so.conf，然后执行 ldconfig。
```
