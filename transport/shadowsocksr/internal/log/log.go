package log

import stdlog "log"

func Warnln(format string, args ...any) {
	stdlog.Printf("shadowsocksr: "+format, args...)
}
