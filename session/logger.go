package session

import (
	"io"
	"log"
	"os"
)

// logger 打印每次操作的输入、输出与判定依据，保证结果可复现、可审计。
var (
	logOut io.Writer = os.Stderr
	logger           = log.New(logOut, "[nego] ", log.Lmicroseconds)
)

// SetLogOutput 重定向协商日志（传 io.Discard 可静默）。
func SetLogOutput(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	logger.SetOutput(w)
}
