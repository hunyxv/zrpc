package zmq

import "time"

type Options struct {
	SndHWM           int
	RcvHWM           int
	Linger           time.Duration
	Immediate        bool
	RouterMandatory  bool
	SendQueueSize    int
	RecvQueueSize    int
	HandshakeTimeout time.Duration
}

func defaultOptions(opts Options) Options {
	if opts.SndHWM == 0 {
		opts.SndHWM = 1000
	}
	if opts.RcvHWM == 0 {
		opts.RcvHWM = 1000
	}
	if opts.Linger == 0 {
		opts.Linger = time.Second
	}
	if opts.SendQueueSize == 0 {
		opts.SendQueueSize = 1024
	}
	if opts.RecvQueueSize == 0 {
		opts.RecvQueueSize = 1024
	}
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = time.Second
	}
	return opts
}
