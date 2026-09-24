// Package inst 实现单个处理实例：已生效版本、当前规则集、
// 按 tag 有序的缓冲区与刷出规则、缓冲上限。
package inst

import (
	"fmt"

	"ontology/rule"
)

// Instance 是一个处理实例。缓冲区按 Tag 不减排列，且每条满足 Tag > ver。
type Instance struct {
	id      int
	ver     int
	rules   map[string]int64
	buf     []rule.Data
	max     int
	checked int // 最近一次应用单个版本时检查过的缓冲区条目数（非导出，不进公开接口）
}

// New 创建编号 id、缓冲上限 max 的实例。
func New(id, max int) *Instance {
	return &Instance{id: id, ver: 0, rules: map[string]int64{}, max: max}
}

// Version 返回已生效版本 v_i。
func (in *Instance) Version() int { return in.ver }

// Buffered 返回当前缓冲条数。
func (in *Instance) Buffered() int { return len(in.buf) }

// BufTags 返回缓冲区各数据 tag 的副本，供不变量核验与演示。
func (in *Instance) BufTags() []int {
	t := make([]int, len(in.buf))
	for i, d := range in.buf {
		t[i] = d.Tag
	}
	return t
}

// CheckAgainst 核验本实例不变量：0<=v_i<=g、缓冲区每条 tag>v_i 且按 tag 不减。
func (in *Instance) CheckAgainst(g int) error {
	if in.ver < 0 || in.ver > g {
		return fmt.Errorf("inst %d: v=%d 越界 [0,%d]", in.id, in.ver, g)
	}
	for j, d := range in.buf {
		if d.Tag <= in.ver || (j > 0 && in.buf[j-1].Tag > d.Tag) {
			return fmt.Errorf("inst %d: 缓冲区违反不变量", in.id)
		}
	}
	return nil
}

// Accept 接收一条数据：Tag==ver 立即处理并返回命中；否则按序入缓冲，
// 缓冲已满返回 ok=false 且状态不变。
func (in *Instance) Accept(d rule.Data) (hits []rule.Hit, ok bool) {
	if d.Tag == in.ver {
		return in.process(d), true
	}
	if len(in.buf) >= in.max {
		return nil, false
	}
	in.buf = append(in.buf, d) // tag 随全局版本单调不减，追加即保持有序
	return nil, true
}

// Apply 应用下一个版本（调用方保证 u 是日志第 ver+1 个版本），
// 随后立即按到达顺序刷出缓冲区中 tag==新 ver 的全部数据。
func (in *Instance) Apply(u rule.Update) []rule.Hit {
	_ = rule.Apply(in.rules, u) // 来自已校验的发布日志，必成功
	in.ver++
	in.checked = 0
	var hits []rule.Hit
	for len(in.buf) > 0 { // 缓冲区按 tag 有序，只看队头
		in.checked++
		if in.buf[0].Tag != in.ver {
			break
		}
		d := in.buf[0]
		in.buf = in.buf[1:]
		hits = append(hits, in.process(d)...)
	}
	return hits
}

// process 用当前已生效规则集（版本恰为 d.Tag）判定一条数据。
func (in *Instance) process(d rule.Data) []rule.Hit {
	var hits []rule.Hit
	for _, id := range rule.Hits(in.rules, d.Val) {
		hits = append(hits, rule.Hit{Key: d.Key, Val: d.Val, RuleID: id, Ver: d.Tag, Inst: in.id})
	}
	return hits
}
