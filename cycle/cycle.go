// Package cycle 把有向环借助临时名展开为不会发生覆盖的线性步骤。
package cycle

import (
	"errors"
	"sort"

	"ontology/plan"
)

// ErrNoTempName 表示在有限次尝试内找不到不冲突的临时名（不覆盖数据的可判定失败）。
var ErrNoTempName = errors.New("no non-conflicting temporary name available")

// tempPrefix 是临时名前缀。
const tempPrefix = "tpm-"

// maxTempAttempts 限制单次生成尝试次数，防止病态输入下死循环。
const maxTempAttempts = 1 << 20

// Expanded 是一个环展开后的结果。
type Expanded struct {
	Nodes []plan.Cycle
	Steps []plan.Step
	Temps []string
}

// TempCount 返回本批使用的临时名数量；按构造它恰好等于环数。
func (e *Expanded) TempCount() int { return len(e.Temps) }

// reserve 是名字占用集合：现有名 ∪ 本批所有新名。
func reserve(existing map[string]struct{}, reqs []plan.Req) map[string]struct{} {
	used := make(map[string]struct{}, len(existing)+len(reqs))
	for n := range existing {
		used[n] = struct{}{}
	}
	for _, r := range reqs {
		used[r.New] = struct{}{}
	}
	return used
}

// newTemp 在 used 中校验 tpm-<k> 候选，冲突即递增重试；成功即占用并返回。
func newTemp(used map[string]struct{}) (string, error) {
	for k := 0; k < maxTempAttempts; k++ {
		cand := tempPrefix + itoa(k)
		if _, taken := used[cand]; !taken {
			used[cand] = struct{}{}
			return cand, nil
		}
	}
	return "", ErrNoTempName
}

func itoa(k int) string {
	if k == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for k > 0 {
		i--
		buf[i] = byte('0' + k%10)
		k /= 10
	}
	return string(buf[i:])
}

// Expand 接收 plan.Build 的结果，把每个环展开。
// 环 Nodes = [p, n1, ..., nk] 表示 p->n1->...->nk->p（p 为字典序最小）。
// 展开：p->t, nk->p, ..., n1->n2, t->n1，共 len+1 步，每步目标位都已腾空。
func Expand(p *plan.Plan, existing map[string]struct{}, reqs []plan.Req) (*Expanded, error) {
	used := reserve(existing, reqs)
	e := &Expanded{Nodes: append([]plan.Cycle(nil), p.Cycles...)}
	// 环按首节点字典序处理，临时名编号因此确定。
	cycles := append([]plan.Cycle(nil), p.Cycles...)
	sort.Slice(cycles, func(i, j int) bool { return cycles[i].Nodes[0] < cycles[j].Nodes[0] })
	for _, cy := range cycles {
		t, err := newTemp(used)
		if err != nil {
			return nil, err
		}
		e.Temps = append(e.Temps, t)
		nodes := cy.Nodes
		pivot, last := nodes[0], nodes[len(nodes)-1]
		e.Steps = append(e.Steps, plan.Step{Old: pivot, New: t})
		e.Steps = append(e.Steps, plan.Step{Old: last, New: pivot})
		for i := len(nodes) - 2; i >= 1; i-- {
			e.Steps = append(e.Steps, plan.Step{Old: nodes[i], New: nodes[i+1]})
		}
		e.Steps = append(e.Steps, plan.Step{Old: t, New: nodes[1]})
	}
	return e, nil
}

// LinearSteps 返回先执行拓扑序非环步骤、再执行各环展开步骤的完整线性序列。
func LinearSteps(p *plan.Plan, e *Expanded) []plan.Step {
	out := append([]plan.Step(nil), p.Steps...)
	out = append(out, e.Steps...)
	return out
}
