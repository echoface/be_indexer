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
		Debugf(format string, v ...interface{})
		Infof(format string, v ...interface{})
		Errorf(format string, v ...interface{})
	}

	// DefaultLogger a console logger use fmt lib
	DefaultLogger struct {
	}
)

func LogDebugIf(condition bool, format string, v ...interface{}) {
	if condition {
		Logger.Debugf(format, v...)
	}
}

func LogInfoIf(condition bool, format string, v ...interface{}) {
	if condition {
		Logger.Infof(format, v...)
	}
}

func LogErrIf(condition bool, format string, v ...interface{}) {
	if condition {
		Logger.Errorf(format, v...)
	}
}

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
