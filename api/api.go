// Package api 是精确一次事务输出的对外入口，依赖方向：api -> olog -> txn。
package api

import (
	"errors"
	"fmt"

	"ontology/olog"
	"ontology/txn"
)

// Rec 即下游日志记录，类型沿依赖链复用 txn.Rec。
type Rec = olog.Rec

// ErrSelfCheck 是自检失败的可判定哨兵错误，细节包在 wrap 里。
var ErrSelfCheck = errors.New("api: self-check failed")

// API 所有状态在进程内存，并发安全。
type API struct {
	l *olog.Log
}

// New 创建一个空的精确一次输出器。
func New() *API {
	return &API{l: olog.New()}
}

// Commit 原子提交一批记录：整批校验、幂等去重、追加日志、刷新视图，
// 返回本次新追加条数。
func (a *API) Commit(txID string, recs []Rec) (int, error) {
	return a.l.Commit(txID, recs)
}

// View 返回物化视图 map[Key]Val 的快照拷贝（后写覆盖）。
func (a *API) View() map[string]int64 {
	return a.l.View()
}

type scKey struct {
	txID string
	seq  int
}
type scKV struct {
	key string
	val int64
}

// SelfCheck 对说明文档第三节的八步内置序列核验四条不变量。
// 它在新建的独立实例上运行（不触碰接收者状态），并用一个独立维护的
// 去重追加日志做批量重算，逐步与被测 View() 对照。
func (a *API) SelfCheck() error {
	c := New()
	type step struct {
		tx    string
		recs  []Rec
		added int
		err   error
	}
	steps := []step{
		{"t1", []Rec{{Seq: 0, Key: "a", Val: 5}, {Seq: 1, Key: "b", Val: 3}, {Seq: 2, Key: "c", Val: 7}}, 3, nil},
		{"t2", []Rec{{Seq: 0, Key: "a", Val: 9}, {Seq: 1, Key: "d", Val: 2}}, 2, nil},
		{"t1", []Rec{{Seq: 3, Key: "e", Val: 4}}, 1, nil},
		{"t1", []Rec{{Seq: 0, Key: "a", Val: 5}}, 0, nil},
		{"t1", []Rec{{Seq: 1, Key: "b", Val: 99}}, 0, nil},
		{"t3", []Rec{{Seq: 0, Key: "f", Val: 1}, {Seq: 1, Key: "", Val: 2}}, 0, txn.ErrEmptyKey},
		{"t2", []Rec{{Seq: 2, Key: "a", Val: 11}}, 1, nil},
		{"t2", []Rec{{Seq: 2, Key: "a", Val: 99}}, 0, nil},
	}
	refLog := []scKV{}                     // 独立的去重追加日志
	refDone := map[scKey]struct{}{}        // 独立的已提交集合
	recompute := func() map[string]int64 { // I1 的批量重算参照
		m := map[string]int64{}
		for _, kv := range refLog {
			m[kv.key] = kv.val
		}
		return m
	}
	mapsEqual := func(x, y map[string]int64) bool {
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if y[k] != v {
				return false
			}
		}
		return true
	}
	totalFresh := 0
	for i, s := range steps {
		n, err := c.Commit(s.tx, s.recs)
		if n != s.added || !errors.Is(err, s.err) {
			return fmt.Errorf("%w: step %d added=%d err=%v, want added=%d err=%v",
				ErrSelfCheck, i+1, n, err, s.added, s.err)
		}
		if s.err != nil { // I4：被拒步骤参照模型不推进，View 必须与之相同
			if !mapsEqual(c.View(), recompute()) {
				return fmt.Errorf("%w: step %d rejected but state changed", ErrSelfCheck, i+1)
			}
			continue
		}
		for _, r := range s.recs { // 独立参照模型自己判重、追加
			k := scKey{s.tx, r.Seq}
			if _, ok := refDone[k]; ok {
				continue
			}
			refDone[k] = struct{}{}
			refLog = append(refLog, scKV{r.Key, r.Val})
			totalFresh++
		}
		if !mapsEqual(c.View(), recompute()) { // I1 逐步一致
			return fmt.Errorf("%w: step %d view != batch recompute", ErrSelfCheck, i+1)
		}
	}
	want := map[string]int64{"a": 11, "b": 3, "c": 7, "d": 2, "e": 4} // f 不存在
	if !mapsEqual(c.View(), want) || totalFresh != 7 {                // I2：共恰 7 条
		return fmt.Errorf("%w: final view=%v fresh=%d", ErrSelfCheck, c.View(), totalFresh)
	}
	// I3：把每个已提交键用不同内容、以逆序逐个重放，必须全部无操作。
	for k := range refDone {
		before := c.View()
		n, err := c.Commit(k.txID, []Rec{{Seq: k.seq, Key: "zzz", Val: -1}})
		if n != 0 || err != nil || !mapsEqual(c.View(), before) {
			return fmt.Errorf("%w: replay %v changed state", ErrSelfCheck, k)
		}
	}
	// 被拒之后仍可继续正常使用。
	if n, err := c.Commit("t3", []Rec{{Seq: 0, Key: "g", Val: 6}}); n != 1 || err != nil || c.View()["g"] != 6 {
		return fmt.Errorf("%w: not usable after rejection", ErrSelfCheck)
	}
	return nil
}
