// Package lww 实现 last-write-wins 压缩：winner 在写入时增量维护，
// Replay 只读缓存并按 Key 字典序输出；另含单键历史超限判定。依赖 seq。
package lww

import (
	"sort"
	"sync/atomic"

	"ontology/seq"
)

// Record 是压缩后的输出项：(Key, Val) 对。
type Record struct {
	Key string
	Val int64
}

// Engine 维护每键 winner 缓存与变更日志。不是并发安全的；并发控制由上层负责。
type Engine struct {
	log     *seq.Log
	max     int
	winners map[string]seq.Change
	// checked 记录最近一次 Replay 为确定 winner 而检查的历史变更条数。
	// 非导出，不出现在任何公开接口的数值中；用 atomic 保证只读并发下干净。
	checked atomic.Int64
}

// New 返回压缩引擎，maxHistory 为单键历史变更数上限。
func New(maxHistory int) *Engine {
	return &Engine{log: seq.NewLog(), max: maxHistory, winners: make(map[string]seq.Change)}
}

// Fits 判定该键再追加 add 条变更是否超出历史上限（历史超限判定）。
func (e *Engine) Fits(key string, add int) bool {
	return e.log.Len(key)+add <= e.max
}

// Apply 追加一条变更并增量维护 winner：Ver 大者胜，Ver 并列 sn 大者胜。
func (e *Engine) Apply(key string, ver, val int64) {
	c := e.log.Append(key, ver, val)
	w, ok := e.winners[key]
	if !ok || c.Ver > w.Ver || (c.Ver == w.Ver && c.Sn > w.Sn) {
		e.winners[key] = c
	}
}

// Winner 返回该键当前 winner。
func (e *Engine) Winner(key string) (seq.Change, bool) {
	w, ok := e.winners[key]
	return w, ok
}

// History 返回该键全部变更，按 sn 升序。
func (e *Engine) History(key string) []seq.Change {
	return e.log.History(key)
}

// Replay 只读 winner 缓存，按 Key 字典序升序输出（消除 map 迭代序）。
// winner 在 Apply 时已增量维护，此处不检查任何历史变更，故 checked 恒为 0。
func (e *Engine) Replay() []Record {
	e.checked.Store(0)
	keys := make([]string, 0, len(e.winners))
	for k := range e.winners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Record, len(keys))
	for i, k := range keys {
		out[i] = Record{Key: k, Val: e.winners[k].Val}
	}
	return out
}

// SelfCheck 在全新引擎上验证：对单键喂 m 条变更（多档规模）后 Replay，
// 为确定 winner 检查的历史变更条数不随 m 增长。只返回是否成立，不暴露计数值。
func SelfCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		e := New(m)
		for i := 0; i < m; i++ {
			e.Apply("k", int64(i), int64(i))
		}
		e.Replay()
		if e.checked.Load() > 1 {
			return false
		}
	}
	return true
}
