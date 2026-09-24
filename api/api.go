// Package api 是对外面层：New、Write、AsOf、Compact、MaxSeq、SelfCheck。依赖 store。
package api

import (
	"fmt"
	"maps"
	"slices"

	"ontology/store"
)

// Val 是写入的值类型。
type Val = store.Val

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey        = store.ErrEmptyKey
	ErrNegativeRead    = store.ErrNegativeRead
	ErrCompacted       = store.ErrCompacted
	ErrNegativeCompact = store.ErrNegativeCompact
)

// Engine 是时间旅行读引擎，并发安全。
type Engine struct {
	st *store.Store
}

// New 返回空引擎。
func New() *Engine { return &Engine{st: store.New()} }

// Write 写入一个版本，返回分配的 Seq；key 为空串拒绝且不分配 Seq。
func (e *Engine) Write(key string, v Val) (int64, error) { return e.st.Write(key, v) }

// AsOf 返回「只应用到 Seq s 为止」的视图。
func (e *Engine) AsOf(s int64) (map[string]Val, error) { return e.st.AsOf(s) }

// Compact 回收 upto 及更早的历史。
func (e *Engine) Compact(upto int64) error { return e.st.Compact(upto) }

// MaxSeq 返回当前已分配的最大 Seq。
func (e *Engine) MaxSeq() int64 { return e.st.MaxSeq() }

// SelfCheck 在独立内部实例上核验四条不变量，不影响本引擎状态，可并发调用。
func (e *Engine) SelfCheck() error {
	type wr struct {
		key string
		val int
	}
	// 确定性操作序列：多 key 交错写入 + 两次 Compact。
	ws := []wr{{"a", 1}, {"b", 10}, {"a", 2}, {"c", 7}, {"b", 11}, {"a", 3}}
	cuts := []int64{2, 4}
	st := store.New()
	var seqs []int64 // 每条写入的 Seq
	for _, w := range ws {
		s, err := st.Write(w.key, w.val)
		if err != nil {
			return fmt.Errorf("selfcheck write: %w", err)
		}
		seqs = append(seqs, s)
	}
	replay := func(s int64) map[string]Val { // 朴素重放：Seq<=s 的写入按序应用
		out := map[string]Val{}
		for i, w := range ws {
			if seqs[i] <= s {
				out[w.key] = w.val
			}
		}
		return out
	}
	// 不变量 2 的参照：compact 前可达位点的视图快照。
	pre := map[int64]map[string]Val{}
	for _, s := range []int64{3, 4, 5, 6, 100} {
		v, err := st.AsOf(s)
		if err != nil {
			return fmt.Errorf("selfcheck pre-asof(%d): %w", s, err)
		}
		pre[s] = v
	}
	upto := int64(-1)
	for _, c := range cuts {
		if err := st.Compact(c); err != nil {
			return fmt.Errorf("selfcheck compact(%d): %w", c, err)
		}
		if c > upto {
			upto = c
		}
	}
	// 不变量 3：不可达即报错。
	for s := int64(0); s <= upto; s++ {
		if _, err := st.AsOf(s); err != ErrCompacted {
			return fmt.Errorf("selfcheck: AsOf(%d) want ErrCompacted, got %v", s, err)
		}
	}
	// 不变量 1+2：可达位点与朴素重放逐 key 相同，且与 compact 前相同。
	for s := upto + 1; s <= 8; s++ {
		got, err := st.AsOf(s)
		if err != nil {
			return fmt.Errorf("selfcheck asof(%d): %w", s, err)
		}
		if !maps.Equal(got, replay(s)) {
			return fmt.Errorf("selfcheck: AsOf(%d)=%v, replay=%v", s, got, replay(s))
		}
		if p, ok := pre[s]; ok && !maps.Equal(got, p) {
			return fmt.Errorf("selfcheck: AsOf(%d) changed by compact", s)
		}
	}
	// 不变量 4：失败不留痕。
	before := st.MaxSeq()
	snap, _ := st.AsOf(before)
	if _, err := st.Write("", 1); err != ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty key got %v", err)
	}
	if _, err := st.AsOf(-1); err != ErrNegativeRead {
		return fmt.Errorf("selfcheck: negative read got %v", err)
	}
	if err := st.Compact(-1); err != ErrNegativeCompact {
		return fmt.Errorf("selfcheck: negative compact got %v", err)
	}
	after, _ := st.AsOf(st.MaxSeq())
	if st.MaxSeq() != before || !maps.Equal(snap, after) {
		return fmt.Errorf("selfcheck: rejected ops changed state")
	}
	// 四类错误互不相同。
	errs := []error{ErrEmptyKey, ErrNegativeRead, ErrCompacted, ErrNegativeCompact}
	for i, a := range errs {
		if slices.Contains(errs[i+1:], a) {
			return fmt.Errorf("selfcheck: duplicate sentinel %v", a)
		}
	}
	return nil
}
