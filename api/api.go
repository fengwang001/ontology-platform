// Package api 是增量左外连接的对外入口：Apply/View/SelfCheck。仅依赖 ljoin。
package api

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/jstate"
	"ontology/ljoin"
)

// Change 重导出上游变更类型；Out 重导出日志条目（RID 为空串表示 NULL）。
type Change = ljoin.Change
type Out = ljoin.Out

// 四类可判定、互不相同的哨兵错误（errors.Is 可直接判定）。
var (
	ErrInvalidChange = ljoin.ErrInvalidChange
	ErrDuplicateID   = ljoin.ErrDuplicateID
	ErrMissingID     = ljoin.ErrMissingID
	ErrTooManyRows   = ljoin.ErrTooManyRows
)

// Joiner 是增量左外连接物化视图的进程内实现。
type Joiner struct {
	mu  sync.RWMutex
	e   *ljoin.Engine
	log []Out // 全部已提交批次的变更日志（按提交顺序）
}

// New 创建两表行数合计上限为 maxRows 的 Joiner（0 表示不限）。
func New(maxRows int) *Joiner { return &Joiner{e: ljoin.New(maxRows)} }

// Apply 顺序处理一批变更并返回本批产出的变更日志（按输出顺序）。
// 任一条被拒则整批不生效，返回哨兵错误，状态与历史日志均不变。
func (j *Joiner) Apply(changes []Change) ([]Out, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	outs, err := j.e.Apply(changes)
	if err != nil {
		return nil, err
	}
	j.log = append(j.log, outs...)
	return append([]Out(nil), outs...), nil
}

// View 返回把全部已提交日志应用到空视图后的物化视图，
// 等价于对当前 L/R 两表做一次朴素左外连接（L ID、R ID 双升序）。
func (j *Joiner) View() []Out {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return naive(j.e.Tables())
}

// naive 对当前两表做朴素嵌套循环左外连接，是不依赖增量日志的独立重算。
func naive(t *jstate.Tables) []Out {
	rows := []Out{}
	for _, l := range t.AllIDs(jstate.SideL) {
		k, _ := t.KeyOf(jstate.SideL, l)
		rs := t.SortedIDs(jstate.SideR, k)
		if len(rs) == 0 {
			rows = append(rows, Out{LID: l})
			continue
		}
		for _, r := range rs {
			rows = append(rows, Out{LID: l, RID: r})
		}
	}
	return rows
}

// SelfCheck 对内置九步序列核验不变量 1/2/3，并核验不变量 4；独立试算，可并发。
func (j *Joiner) SelfCheck() error {
	e := ljoin.New(0)
	ms := map[[2]string]int{} // (LID,RID) 多重集计数，存在即 1
	for st, ch := range builtin {
		outs, err := e.Apply([]Change{ch})
		if err != nil {
			return fmt.Errorf("selfcheck step %d: %w", st+1, err)
		}
		for _, o := range outs { // 不变量 2：'+' 前 0、'-' 前 1
			k := [2]string{o.LID, o.RID}
			if (o.Op == '+' && ms[k] != 0) || (o.Op == '-' && ms[k] != 1) {
				return fmt.Errorf("step %d: bad count %v=%d", st+1, k, ms[k])
			} else if o.Op == '+' {
				ms[k] = 1
			} else {
				delete(ms, k)
			}
		}
		if got, want := material(ms), naive(e.Tables()); !reflect.DeepEqual(got, want) { // 不变量 1
			return fmt.Errorf("step %d: %v != batch %v", st+1, got, want)
		}
		for k := range ms { // 不变量 3：NULL 行与任一配对行互斥
			if k[1] != "" && ms[[2]string{k[0], ""}] == 1 {
				return fmt.Errorf("step %d: %s NULL/matched coexist", st+1, k[0])
			}
		}
	}
	// 不变量 4：被拒批次整批不留痕（total 仍为 0），之后再提交一条可成功（total=1）。
	e4 := ljoin.New(1)
	bad := []Change{{Side: jstate.SideL, Op: '+', ID: "a", Key: "x"}, {Side: jstate.SideR, Op: '+', ID: "b", Key: "x"}}
	_, err := e4.Apply(bad)
	_, err2 := e4.Apply(bad[:1])
	if err != ljoin.ErrTooManyRows || err2 != nil || e4.Tables().Total() != 1 {
		return fmt.Errorf("selfcheck inv4: trace left or unusable after rejection")
	}
	return nil
}

// builtin 是第三节唯一推导点的九条变更。
var builtin = func() []Change {
	c := func(s jstate.Side, op byte, id, k string) Change {
		return Change{Side: s, Op: op, ID: id, Key: k}
	}
	return []Change{
		c(jstate.SideR, '+', "r1", "x"), c(jstate.SideL, '+', "l1", "x"),
		c(jstate.SideL, '+', "l2", "y"), c(jstate.SideL, '+', "l3", "y"),
		c(jstate.SideR, '+', "r2", "y"), c(jstate.SideR, '+', "r3", "y"),
		c(jstate.SideR, '-', "r2", ""), c(jstate.SideR, '-', "r3", ""),
		c(jstate.SideR, '-', "r1", ""),
	}
}()

// material 把多重集计数物化为 (LID,RID) 双升序的行集合。
func material(ms map[[2]string]int) []Out {
	ks := make([][2]string, 0, len(ms))
	for k := range ms {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(a, b int) bool {
		return ks[a][0] < ks[b][0] || ks[a][0] == ks[b][0] && ks[a][1] < ks[b][1]
	})
	rows := make([]Out, 0, len(ks))
	for _, k := range ks {
		rows = append(rows, Out{LID: k[0], RID: k[1]})
	}
	return rows
}
