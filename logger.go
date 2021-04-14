package be_indexer

import "fmt"

const (
	DebugLevel = iota
	InfoLevel
	ErrorLevel
)

var (
	LogLevel int           = DebugLevel // control defaultLogger log level
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
	if LogLevel > DebugLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	if LogLevel > InfoLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	if LogLevel > ErrorLevel {
		return
	}
	fmt.Printf(format, v...)
}
