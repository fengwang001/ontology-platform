// Package api 对外门面：物化视图的构造、喂数、读取与自检。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/tbucket"
	"ontology/tpart"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrBadSize  = tpart.ErrBadSize
	ErrBadR     = tpart.ErrBadR
	ErrEmptyKey = tpart.ErrEmptyKey
)

// Event 是一条到达事件。
type Event = tpart.Event

// View 是 (Key -> 桶键 -> 计数) 的物化视图快照。
type View = map[string]map[int64]int64

// Materialized 是按时间分区、滚动保留的物化视图，并发安全。
type Materialized struct {
	p    *tpart.Partition
	size int64
	r    int64
}

// New 构造视图；size、R 非正时整体失败并返回可判定错误。
func New(size, r int64) (*Materialized, error) {
	p, err := tpart.New(size, r)
	if err != nil {
		return nil, err
	}
	return &Materialized{p: p, size: size, r: r}, nil
}

// Feed 处理一批事件；任一条被拒则整批不生效。
func (m *Materialized) Feed(evs []Event) error { return m.p.Feed(evs) }

// View 返回当前视图快照。
func (m *Materialized) View() View { return m.p.View() }

// Dropped 返回累计被丢弃的事件总数。
func (m *Materialized) Dropped() int64 { return m.p.Dropped() }

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil；不改动 m 的状态。
func (m *Materialized) SelfCheck() error {
	// 不变量 2：负时间戳桶归属。
	for _, c := range []struct{ a, b, want int64 }{{-12, 10, -2}, {-1, 10, -1}, {-10, 10, -1}, {0, 10, 0}, {9, 10, 0}, {10, 10, 1}} {
		if got := tbucket.FloorDiv(c.a, c.b); got != c.want {
			return fmt.Errorf("selfcheck: floorDiv(%d,%d)=%d，应为 %d", c.a, c.b, got, c.want)
		}
	}
	// 不变量 1+3：八步序列的视图与批量重算一致、Dropped 正确、保留窗口合法。
	seq := []Event{{TS: 5, Key: "k"}, {TS: -12, Key: "k"}, {TS: 0, Key: "k"}, {TS: 10, Key: "k"}, {TS: -1, Key: "k"}, {TS: 20, Key: "k"}, {TS: 9, Key: "k"}, {TS: 30, Key: "k"}}
	sc, err := New(10, 3)
	if err != nil {
		return err
	}
	if err := sc.Feed(seq); err != nil {
		return err
	}
	if !reflect.DeepEqual(sc.View(), batchRecompute(seq, 10, 3)) {
		return errors.New("selfcheck: 视图与批量重算不一致")
	}
	if sc.Dropped() != 5 {
		return fmt.Errorf("selfcheck: Dropped=%d，应为 5", sc.Dropped())
	}
	for k := range sc.View()["k"] {
		if k < 1 || k > 3 { // cur=3，窗口 [cur-R+1, cur]=[1,3]
			return fmt.Errorf("selfcheck: 桶 %d 越出保留窗口 [1,3]", k)
		}
	}
	// 不变量 4：三类拒绝互不相同且不留痕。
	if _, e1 := New(0, 1); e1 != ErrBadSize {
		return errors.New("selfcheck: size 非正未返回 ErrBadSize")
	}
	if _, e2 := New(1, 0); e2 != ErrBadR {
		return errors.New("selfcheck: R 非正未返回 ErrBadR")
	}
	before, beforeDrop := sc.View(), sc.Dropped()
	if e3 := sc.Feed([]Event{{TS: 40, Key: "k"}, {TS: 50, Key: ""}}); e3 != ErrEmptyKey {
		return errors.New("selfcheck: 空 Key 未返回 ErrEmptyKey")
	}
	if !reflect.DeepEqual(sc.View(), before) || sc.Dropped() != beforeDrop {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}

// batchRecompute 只取被保留（未被丢弃）的事件，按 (Key, floor 桶) 分组计数。
func batchRecompute(evs []Event, size, r int64) View {
	ks := make([]int64, len(evs))
	late := make([]bool, len(evs))
	var cur int64
	hasCur := false
	for i, e := range evs {
		ks[i] = tbucket.Key(e.TS, size)
		if hasCur && ks[i] < cur-r+1 {
			late[i] = true
			continue
		}
		if !hasCur || ks[i] > cur {
			cur, hasCur = ks[i], true
		}
	}
	out := View{}
	for i, e := range evs {
		if late[i] || ks[i] < cur-r+1 { // 迟到丢弃或所在桶已被清理
			continue
		}
		if out[e.Key] == nil {
			out[e.Key] = map[int64]int64{}
		}
		out[e.Key][ks[i]]++
	}
	return out
}
