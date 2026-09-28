package txnset

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	logMu     sync.Mutex
	logOutput io.Writer = io.Discard
)

// SetLogOutput 设置组件日志输出位置，传 nil 等价于 io.Discard。
// 日志记录每次解析、合并、差集的输入、规范文本与判定依据。
// 可在并发运行期间安全切换。
func SetLogOutput(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	logMu.Lock()
	logOutput = w
	logMu.Unlock()
}

// logf 输出一行带时间戳的组件日志。
func logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	logMu.Lock()
	fmt.Fprintf(logOutput, "%s txnset: %s\n", time.Now().Format("2006-01-02T15:04:05.000000"), line)
	logMu.Unlock()
}
