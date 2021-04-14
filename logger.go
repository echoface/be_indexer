package be_indexer

import "fmt"

const (
	debugLevel = iota
	infoLevel
	errorLevel
)

var (
	LogLevel int           = debugLevel // control defaultLogger log level
	Logger   BEIndexLogger = &DefaultLogger{}
)

type (
	BEIndexLogger interface {
		Debugf(fmt string, v ...interface{})
		Infof(fmt string, v ...interface{})
		Errorf(fmt string, v ...interface{})
	}
	DefaultLogger struct {
	}
)

func (l *DefaultLogger) Debugf(format string, v ...interface{}) {
	if LogLevel > debugLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	if LogLevel > infoLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	if LogLevel > errorLevel {
		return
	}
	fmt.Printf(format, v...)
}
