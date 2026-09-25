// Package api 是 exactly-once 事务输出的对外门面，依赖 olog（传递依赖 txn）。
package api

import (
	"fmt"

	"ontology/olog"
	"ontology/txn"
)

// Rec 是对外暴露的输出记录。
type Rec = txn.Rec

// 可判定哨兵错误（与 txn 中的错误同一实例，errors.Is 可直接判定）。
var (
	ErrEmptyTxID    = txn.ErrEmptyTxID
	ErrEmptyKey     = txn.ErrEmptyKey
	ErrDuplicateSeq = txn.ErrDuplicateSeq
)

// Store 是去重追加日志的进程内存实现，并发安全。
type Store struct {
	log *olog.Log
}

// New 创建空 Store。
func New() *Store { return &Store{log: olog.New()} }

// Commit 原子提交一批记录：整批校验通过后，按 (txID,Seq) 幂等追加，
// 返回本次新追加条数；任一记录不合法则整批拒绝且不留痕。
func (s *Store) Commit(txID string, recs []Rec) (int, error) {
	return s.log.Commit(txID, recs)
}

// View 返回物化视图 map[Key]Val 的副本（后写覆盖）。
func (s *Store) View() map[string]int64 { return s.log.View() }

// SelfCheck 在内置提交序列上核验四条不变量（含大 m 下扫描量为常数），
// 只返回成败，不暴露任何内部计数；核验在独立实例上进行，不影响本 Store。
func (s *Store) SelfCheck() error { return selfCheck() }

func selfCheck() error {
	l := olog.New()
	r := func(seq int, key string, val int64) Rec { return Rec{Seq: seq, Key: key, Val: val} }
	type step struct {
		tx   string
		recs []Rec
		want int
		view map[string]int64 // nil 表示该步视图应与上一步相同
	}
	steps := []step{
		{"t1", []Rec{r(0, "a", 5), r(1, "b", 3), r(2, "c", 7)}, 3, map[string]int64{"a": 5, "b": 3, "c": 7}},
		{"t2", []Rec{r(0, "a", 9), r(1, "d", 2)}, 2, map[string]int64{"a": 9, "b": 3, "c": 7, "d": 2}},
		{"t1", []Rec{r(3, "e", 4)}, 1, map[string]int64{"a": 9, "b": 3, "c": 7, "d": 2, "e": 4}},
		{"t1", []Rec{r(0, "a", 5)}, 0, nil},
		{"t1", []Rec{r(1, "b", 99)}, 0, nil},
		{"t3", []Rec{r(0, "f", 1), r(1, "", 2)}, 0, nil},
		{"t2", []Rec{r(2, "a", 11)}, 1, map[string]int64{"a": 11, "b": 3, "c": 7, "d": 2, "e": 4}},
		{"t2", []Rec{r(2, "a", 99)}, 0, nil},
	}
	prev := map[string]int64{}
	for i, st := range steps {
		before := l.View()
		n, err := l.Commit(st.tx, st.recs)
		if n != st.want {
			return fmt.Errorf("step %d: appended=%d want %d", i+1, n, st.want)
		}
		if i == 5 { // 第 6 步：整批拒绝
			if err != ErrEmptyKey {
				return fmt.Errorf("step 6: want ErrEmptyKey, got %v", err)
			}
			if !equalView(l.View(), before) {
				return fmt.Errorf("step 6: rejected batch left a trace")
			}
			prev = before
			continue
		}
		if err != nil {
			return fmt.Errorf("step %d: unexpected error %v", i+1, err)
		}
		want := st.view
		if want == nil {
			want = prev
		}
		if !equalView(l.View(), want) {
			return fmt.Errorf("step %d: view mismatch: %v", i+1, l.View())
		}
		prev = l.View()
	}
	if _, has := prev["f"]; has {
		return fmt.Errorf("f must never exist")
	}
	if prev["a"] != 11 || prev["b"] != 3 || prev["e"] != 4 {
		return fmt.Errorf("final values wrong: %v", prev)
	}
	return olog.CheckReplayLookup()
}

func equalView(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
