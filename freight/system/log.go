package system

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// JSONLogger 将每次操作以一行 JSON 打印：序号、操作名、输入、输出/错误、判定依据。
// 内容只依赖操作序列本身（无墙钟时间、无 map 遍历、无指针地址），
// 因而相同序列重放得到字节级一致的日志。
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLogger 创建日志器。
func NewJSONLogger(w io.Writer) *JSONLogger { return &JSONLogger{w: w} }

// Log 实现 Logger。
func (l *JSONLogger) Log(seq int64, op string, input, output string) {
	b, _ := json.Marshal(map[string]any{
		"seq":    seq,
		"op":     op,
		"input":  rawJSON(input),
		"output": rawJSON(output),
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, string(b))
}

// rawJSON 允许调用方直接传入 JSON 文本；非 JSON 文本退化为字符串。
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) {
	s := string(r)
	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	return json.Marshal(s)
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `"<marshal error: ` + err.Error() + `>"`
	}
	return string(b)
}
