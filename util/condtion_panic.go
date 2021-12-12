package util

import "fmt"

func PanicIf(cond bool, format string, v ...interface{}) {
	if !cond {
		return
	}
	panic(fmt.Errorf(format, v...))
}

func PanicIfErr(err error, format string, v ...interface{}) {
	if err == nil {
		return
	}
	panic(fmt.Errorf(format, v...))
}
