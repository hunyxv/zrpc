package zrpc

import "log"

type Logger interface {
	Debug(args ...any)
	Debugf(format string, args ...any)
	Info(args ...any)
	Infof(format string, args ...any)
	Warn(args ...any)
	Warnf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
}

type logger struct{}

func (*logger) Debug(args ...any) {
	log.Println(args...)
}
func (*logger) Debugf(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Info(args ...any) {
	log.Println(args...)
}
func (*logger) Infof(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Warn(args ...any) {
	log.Println(args...)
}
func (*logger) Warnf(format string, args ...any) {
	log.Printf(format, args...)
}
func (*logger) Error(args ...any) {
	log.Println(args...)
}
func (*logger) Errorf(format string, args ...any) {
	log.Printf(format, args...)
}

func (*logger) Fatal(args ...any) {
	log.Fatal(args...)
}
