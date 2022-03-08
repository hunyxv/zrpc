package zrpc

import "log"

type Logger interface {
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
}

type logger struct{}

func (*logger) Debug(args ...interface{}) {
	log.Println(args...)
}
func (*logger) Debugf(format string, args ...interface{}) {
	log.Printf(format, args...)
}
func (*logger) Info(args ...interface{}) {
	log.Println(args...)
}
func (*logger) Infof(format string, args ...interface{}) {
	log.Printf(format, args...)
}
func (*logger) Warn(args ...interface{}) {
	log.Println(args...)
}
func (*logger) Warnf(format string, args ...interface{}) {
	log.Printf(format, args...)
}
func (*logger) Error(args ...interface{}) {
	log.Println(args...)
}
func (*logger) Errorf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func(*logger) Fatal(args ...interface{}) {
	log.Fatal(args...)
}
