// Package api 对外提供增量左外连接的物化视图维护。
package api

import (
	"fmt"
	"maps"
	"sync"

	"ontology/jstate"
	"ontology/ljoin"
)

// VRow 是物化视图的一行，R 为空串表示 NULL。
type VRow struct{ L, R string }

// J 维护 L LEFT OUTER JOIN R 的变更日志与物化视图。
type J struct {
	mu  sync.RWMutex
	st  *jstate.State
	max int
	log []ljoin.Out
}

func New(maxRows int) *J { return &J{st: jstate.New(), max: maxRows} }

// Apply 按顺序处理一批变更；任一条被拒则整批不生效。
func (j *J) Apply(cs []ljoin.Change) ([]ljoin.Out, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.st.Clone() // 在副本上处理，失败即整体丢弃
	p := ljoin.New(st, j.max)
	var outs []ljoin.Out
	for _, c := range cs {
		o, err := p.ApplyOne(c)
		if err != nil {
			return nil, err
		}
		outs = append(outs, o...)
	}
	j.st = st
	j.log = append(j.log, outs...)
	return outs, nil
}

// counts 重放变更日志为多重集并剔除零计数（调用方须持锁）。
func (j *J) counts() map[VRow]int {
	cnt := map[VRow]int{}
	for _, o := range j.log {
		d := 1
		if !o.Plus {
			d = -1
		}
		cnt[VRow{o.LID, o.RID}] += d
	}
	maps.DeleteFunc(cnt, func(_ VRow, n int) bool { return n == 0 })
	return cnt
}

// View 返回下游应用全部变更日志后的物化视图（仅含计数为 1 的行）。
func (j *J) View() map[VRow]int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.counts()
}

// checkLog 核验不变量 2、3：逐前缀计数 0/1、NULL 行互斥、结尾形态唯一。
func (j *J) checkLog() error {
	cnt, null, match := map[VRow]int{}, map[string]int{}, map[string]int{}
	for i, o := range j.log {
		r, d := VRow{o.LID, o.RID}, 1
		if !o.Plus {
			d = -1
		}
		if cnt[r]+d < 0 || cnt[r]+d > 1 {
			return fmt.Errorf("前缀 %d: 行 %v 计数越界", i, r)
		}
		cnt[r] += d
		if o.RID == "" {
			null[o.LID] += d
		} else {
			match[o.LID] += d
		}
		if null[o.LID] > 0 && match[o.LID] > 0 {
			return fmt.Errorf("前缀 %d: %s 同时存在 NULL 行与匹配行", i, o.LID)
		}
	}
	for _, l := range j.st.AllL() { // 每个存活 L 行恰属一种形态
		if (null[l.ID] == 1) == (match[l.ID] > 0) {
			return fmt.Errorf("L 行 %s 形态不唯一", l.ID)
		}
	}
	return nil
}

func checkOne(j *J) error {
	j.mu.RLock()
	defer j.mu.RUnlock()
	want := map[VRow]int{}
	for _, l := range j.st.AllL() {
		rids := j.st.RIDs(l.Key)
		if len(rids) == 0 {
			want[VRow{l.ID, ""}]++
		}
		for _, rid := range rids {
			want[VRow{l.ID, rid}]++
		}
	}
	if !maps.Equal(j.counts(), want) {
		return fmt.Errorf("视图与批量重算不一致")
	}
	return j.checkLog()
}

func ch(s ljoin.Side, o ljoin.Op, id, k string) ljoin.Change {
	return ljoin.Change{Side: s, Op: o, ID: id, Key: k}
}

// SelfCheck 核验当前实例及内置变更序列的四条不变量，全部通过返回 nil。
func (j *J) SelfCheck() error {
	if err := checkOne(j); err != nil {
		return fmt.Errorf("当前实例: %w", err)
	}
	L, R, I, D := ljoin.L, ljoin.R, ljoin.Ins, ljoin.Del
	seqs := [][]ljoin.Change{
		{ch(R, I, "r1", "x"), ch(L, I, "l1", "x"), ch(L, I, "l2", "y"), ch(L, I, "l3", "y"),
			ch(R, I, "r2", "y"), ch(R, I, "r3", "y"), ch(R, D, "r2", ""), ch(R, D, "r3", ""), ch(R, D, "r1", "")},
		{ch(L, I, "a", "k"), ch(L, I, "b", "k"), ch(R, I, "u", "k"), ch(R, I, "v", "k"),
			ch(L, D, "a", ""), ch(R, D, "u", ""), ch(L, D, "b", ""), ch(R, D, "v", "")},
	}
	for i, seq := range seqs {
		f := New(1000)
		if _, err := f.Apply(seq); err != nil {
			return fmt.Errorf("序列 %d: %w", i, err)
		}
		if err := checkOne(f); err != nil {
			return fmt.Errorf("序列 %d: %w", i, err)
		}
		before := f.View()
		_, err := f.Apply([]ljoin.Change{ch(L, I, "bad", "")})
		if err == nil || !maps.Equal(f.View(), before) { // 不变量 4：失败不留痕
			return fmt.Errorf("序列 %d: 被拒变更留下痕迹或未被拒(%v)", i, err)
		}
	}
	return nil
}
