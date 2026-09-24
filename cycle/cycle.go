// Package cycle 用临时名破解环形重命名。
package cycle

import (
	"errors"
	"fmt"

	"ontology/plan"
)

// ErrNoFreeTemp 所有候选临时名都被现有名/目标名占用（绝不覆盖数据）。
var ErrNoFreeTemp = errors.New("cycle: no free temporary name available")

// TempPrefix 是临时名的固定前缀，便于测试人为占用。
const TempPrefix = ".rename-tmp-"

// Breaker 为若干互不相交的环生成破环步骤。
type Breaker struct {
	// taken 是初始现有名与全部请求新名的并集（临时名不得与之冲突）。
	taken     map[string]struct{}
	tempCount int
	attempts  int
}

// NewBreaker 用「初始现有名 ∪ 全部请求新名」构造占用集合。
func NewBreaker(existing []string, reqs []plan.Req, maxAttempts int) *Breaker {
	b := &Breaker{taken: map[string]struct{}{}, attempts: maxAttempts}
	for _, n := range existing {
		b.taken[n] = struct{}{}
	}
	for _, r := range reqs {
		b.taken[r.New] = struct{}{}
	}
	return b
}

// TempCount 返回已分配的临时名数量，精确等于环的个数。
func (b *Breaker) TempCount() int { return b.tempCount }

func (b *Breaker) allocTemp() (string, error) {
	for i := 0; i < b.attempts; i++ {
		cand := fmt.Sprintf("%s%d", TempPrefix, b.tempCount*b.attempts+i)
		if _, used := b.taken[cand]; !used {
			b.taken[cand] = struct{}{}
			b.tempCount++
			return cand, nil
		}
	}
	return "", ErrNoFreeTemp
}

// Break 为单个环（plan.Analyze 给出的、已按最小成员开头定向的成员序列）
// 生成破环步骤。环 c = [v0,v1,...,v(n-1)] 表示 v_i 的新名是 v_(i+1)，
// v(n-1) 的新名是 v0。步骤为：
// v0→T, v(n-1)→v0, v(n-2)→v(n-1), …, v1→v2, T→v1。
func (b *Breaker) Break(cyc []string) ([]plan.Step, error) {
	n := len(cyc)
	if n == 0 {
		return nil, nil
	}
	if n == 1 {
		return []plan.Step{{Old: cyc[0], New: cyc[0]}}, nil
	}
	tmp, err := b.allocTemp()
	if err != nil {
		return nil, err
	}
	out := make([]plan.Step, 0, n+1)
	out = append(out, plan.Step{Old: cyc[0], New: tmp})
	for i := n - 1; i >= 1; i-- {
		out = append(out, plan.Step{Old: cyc[i], New: cyc[(i + 1) % n]})
	}
	out = append(out, plan.Step{Old: tmp, New: cyc[1]})
	return out, nil
}

// BreakAll 依次破掉全部环；任一环无法取得临时名则整体失败（尚未产生修改）。
func (b *Breaker) BreakAll(cycles [][]string) ([]plan.Step, error) {
	var all []plan.Step
	for _, c := range cycles {
		st, err := b.Break(c)
		if err != nil {
			return nil, err
		}
		all = append(all, st...)
	}
	return all, nil
}
