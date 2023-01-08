package util

import "fmt"

// PanicIf a panic helper to invoke panic when cond is true
// user/client need responsible for the work of logging/print error detail
func PanicIf(cond bool, format string, v ...interface{}) {
	if !cond {
		return
	}
	panic(fmt.Errorf(format, v...))
}

// PanicIfErr a panic helper to invoke panic when err is not nil
// user/client need responsible for the work of logging/print error detail
func PanicIfErr(err error, format string, v ...interface{}) {
	if err == nil {
		return
	}
	errStr := fmt.Sprintf("err:%v", err)
	panic(fmt.Errorf(errStr+format, v...))
}
