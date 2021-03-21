package beindexer

import "fmt"

var (
	logLevel int // control defaultLogger log level

	Logger BEIndexLogger = &DefaultLogger{}
)

const (
	errorLevel = iota
	infoLevel
	debugLevel
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
	if logLevel > debugLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	if logLevel > infoLevel {
		return
	}
	fmt.Printf(format, v...)
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	if logLevel > errorLevel {
		return
	}
	fmt.Printf(format, v...)
}
