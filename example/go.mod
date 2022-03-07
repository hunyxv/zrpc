module example

go 1.17

require (
	github.com/hunyxv/zrpc v0.0.0-00010101000000-000000000000
	github.com/pebbe/zmq4 v1.2.8
	github.com/vmihailenco/msgpack/v5 v5.3.5
	go.opentelemetry.io/otel v1.4.1
	go.opentelemetry.io/otel/exporters/jaeger v1.4.1
	go.opentelemetry.io/otel/sdk v1.4.1
	go.opentelemetry.io/otel/trace v1.4.1
)

require (
	github.com/go-logr/logr v1.2.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/hunyxv/utils v0.0.0-20220221134715-bf42549b7954 // indirect
	github.com/panjf2000/ants/v2 v2.4.8 // indirect
	github.com/pborman/uuid v1.2.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/sys v0.0.0-20210423185535-09eb48e85fd7 // indirect
)

replace github.com/hunyxv/zrpc => ../
