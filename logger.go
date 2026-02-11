package be_indexer

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

const (
	DebugLevel = iota
	InfoLevel
	ErrorLevel
)

var (
	LogLevel int                = InfoLevel // control defaultLogger log level
	Logger   core.BEIndexLogger = &DefaultLogger{}
)

type (
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

func LogIfErr(err error, format string, v ...interface{}) {
	if err == nil {
		return
	}
	Logger.Errorf(format, v...)
	Logger.Errorf("Error:%s", err.Error())
}

func LogErr(format string, v ...interface{}) {
	Logger.Errorf(format, v...)
}

func LogInfo(format string, v ...interface{}) {
	Logger.Infof(format, v...)
}

func LogDebug(format string, v ...interface{}) {
	Logger.Debugf(format, v...)
}

func (l *DefaultLogger) Debugf(format string, v ...interface{}) {
	if LogLevel > DebugLevel {
		return
	}
	fmt.Printf(format, v...)
	fmt.Println()
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	if LogLevel > InfoLevel {
		return
	}
	fmt.Printf(format, v...)
	fmt.Println()
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	if LogLevel > ErrorLevel {
		return
	}
	fmt.Printf(format, v...)
	fmt.Println()
}
