package anomaly

import (
	"encoding/json"
	"io"
	"sort"
	"sync"
)

// Logger 是并发安全的判定入口：所有写入由内部互斥锁串行化，
// 同一份历史从多个 goroutine 调用会得到逐字节一致的判定记录。
// 零值不可用，请用 NewLogger 构造。
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w}
}

// Analyze 执行判定并记录规范化输入、输出与判定依据，返回与
// anomaly.Analyze 完全相同的结果。
func (l *Logger) Analyze(h History) Result {
	r := Analyze(h)
	normalized := canonical(h)
	in, _ := json.Marshal(normalized)
	out, _ := json.Marshal(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write([]byte("input=" + string(in) + "\n"))
	_, _ = l.w.Write([]byte("output=" + string(out) + "\n"))
	_, _ = l.w.Write([]byte("reason=" + r.Reason + r.Reject + "\n\n"))
	return r
}

// canonical 返回与输入排列无关的规范化形式：事务按编号排序、
// 每事务操作保持自身时间序（写序号语义不能打乱）、键排序、
// 版本次序本身为有向序列保持不动。
func canonical(h History) History {
	c := History{Order: make(map[string][][2]int, len(h.Order))}
	txns := append([]Txn(nil), h.Txns...)
	sort.Slice(txns, func(i, j int) bool { return txns[i].ID < txns[j].ID })
	c.Txns = txns
	keys := make([]string, 0, len(h.Order))
	for k := range h.Order {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Order[k] = append([][2]int(nil), h.Order[k]...)
	}
	return c
}
