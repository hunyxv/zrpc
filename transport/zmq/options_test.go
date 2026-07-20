package zmq

import (
	"testing"
	"time"
)

func TestOptionsDefaults(t *testing.T) {
	opts := defaultOptions(Options{})

	if opts.SndHWM != 1000 {
		t.Fatalf("SndHWM = %d, want 1000", opts.SndHWM)
	}
	if opts.RcvHWM != 1000 {
		t.Fatalf("RcvHWM = %d, want 1000", opts.RcvHWM)
	}
	if opts.Linger != time.Second {
		t.Fatalf("Linger = %v, want %v", opts.Linger, time.Second)
	}
	if opts.SendQueueSize != 1024 {
		t.Fatalf("SendQueueSize = %d, want 1024", opts.SendQueueSize)
	}
	if opts.RecvQueueSize != 1024 {
		t.Fatalf("RecvQueueSize = %d, want 1024", opts.RecvQueueSize)
	}
	if opts.SendQueueBytes != 64<<20 {
		t.Fatalf("SendQueueBytes = %d, want %d", opts.SendQueueBytes, int64(64<<20))
	}
	if opts.ControlQueueSize != 128 {
		t.Fatalf("ControlQueueSize = %d, want 128", opts.ControlQueueSize)
	}
	if opts.HandshakeTimeout != time.Second {
		t.Fatalf("HandshakeTimeout = %v, want %v", opts.HandshakeTimeout, time.Second)
	}
	if opts.HeartbeatInterval != 10*time.Second {
		t.Fatalf("HeartbeatInterval = %v, want %v", opts.HeartbeatInterval, 10*time.Second)
	}
	if opts.PeerTimeout != 30*time.Second {
		t.Fatalf("PeerTimeout = %v, want %v", opts.PeerTimeout, 30*time.Second)
	}
	if opts.CloseHandshakeTimeout != 5*time.Second {
		t.Fatalf("CloseHandshakeTimeout = %v, want %v", opts.CloseHandshakeTimeout, 5*time.Second)
	}
}

func TestOptionsDefaultsOnlyZeroValues(t *testing.T) {
	opts := defaultOptions(Options{
		SndHWM:                -1,
		RcvHWM:                -1,
		Linger:                -1,
		SendQueueSize:         -1,
		RecvQueueSize:         -1,
		SendQueueBytes:        -1,
		ControlQueueSize:      -1,
		HandshakeTimeout:      -1,
		HeartbeatInterval:     -1,
		PeerTimeout:           -1,
		CloseHandshakeTimeout: -1,
	})

	if opts.SndHWM != -1 || opts.RcvHWM != -1 || opts.Linger != -1 ||
		opts.SendQueueSize != -1 || opts.RecvQueueSize != -1 || opts.SendQueueBytes != -1 ||
		opts.ControlQueueSize != -1 || opts.HandshakeTimeout != -1 ||
		opts.HeartbeatInterval != -1 || opts.PeerTimeout != -1 || opts.CloseHandshakeTimeout != -1 {
		t.Fatalf("defaultOptions() changed negative values: %+v", opts)
	}
}

func TestOptionsValidateRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "snd hwm", mutate: func(opts *Options) { opts.SndHWM = -1 }},
		{name: "rcv hwm", mutate: func(opts *Options) { opts.RcvHWM = -1 }},
		{name: "send queue size", mutate: func(opts *Options) { opts.SendQueueSize = -1 }},
		{name: "recv queue size", mutate: func(opts *Options) { opts.RecvQueueSize = -1 }},
		{name: "send queue bytes", mutate: func(opts *Options) { opts.SendQueueBytes = -1 }},
		{name: "control queue size", mutate: func(opts *Options) { opts.ControlQueueSize = -1 }},
		{name: "handshake timeout", mutate: func(opts *Options) { opts.HandshakeTimeout = -1 }},
		{name: "heartbeat interval", mutate: func(opts *Options) { opts.HeartbeatInterval = -1 }},
		{name: "peer timeout", mutate: func(opts *Options) { opts.PeerTimeout = -1 }},
		{name: "close handshake timeout", mutate: func(opts *Options) { opts.CloseHandshakeTimeout = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultOptions(Options{})
			tt.mutate(&opts)
			if err := opts.validate(); err == nil {
				t.Fatal("validate() error = nil, want non-nil")
			}
		})
	}
}

func TestOptionsValidateAllowsNegativeLinger(t *testing.T) {
	opts := defaultOptions(Options{Linger: -1})
	if err := opts.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestOptionsValidatePeerTimeoutBoundary(t *testing.T) {
	opts := defaultOptions(Options{
		HeartbeatInterval: 10 * time.Second,
		PeerTimeout:       20 * time.Second,
	})
	if err := opts.validate(); err != nil {
		t.Fatalf("validate() at boundary error = %v", err)
	}

	opts.PeerTimeout--
	if err := opts.validate(); err == nil {
		t.Fatal("validate() below boundary error = nil, want non-nil")
	}
}
