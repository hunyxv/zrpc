package zrpc

import "github.com/sirupsen/logrus"

var logger *logrus.Entry

func init() {
	logger = logrus.WithField("package", "go-zrpc")
	logger.Logger.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02 15:04:05"})
	logger.Logger.SetReportCaller(true)
}
