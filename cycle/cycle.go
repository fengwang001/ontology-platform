// Package cycle 实现环形重命名的破环：每个环借用一个临时名。
package cycle

import (
	"errors"
	"strconv"
)

// ErrNoTemp 表示在重试上限内找不到不冲突的临时名。
var ErrNoTemp = errors.New("cycle: no collision-free temporary name")

// maxTries 是临时名生成的重试上限，超过后返回 ErrNoTemp 而不是冒险覆盖。
const maxTries = 1 << 20

// Step 是一步重命名：把 Old 改名为 New。
type Step struct {
	Old string
	New string
}

// Gen 是确定性的临时名生成器：候选形如 "\x00renametmp<N>"，N 从 0 递增。
// 候选与现有名或目标名冲突时自动重试。
type Gen struct {
	n int
}

// Next 返回下一个不与 taken 冲突的临时名。
func (g *Gen) Next(taken func(string) bool) (string, error) {
	for range maxTries {
		cand := "\x00renametmp" + strconv.Itoa(g.n)
		g.n++
		if !taken(cand) {
			return cand, nil
		}
	}
	return "", ErrNoTemp
}

// Break 把一个环拆成安全的执行序列。cyc 必须按环序给出：
// cyc[i].New == cyc[i+1].Old 且最后一个请求的 New == cyc[0].Old。
// 返回 len(cyc)+1 步：先把 cyc[0].Old 挪到 tmp，再逆序执行其余请求，
// 最后把 tmp 挪到 cyc[0].New。每步的目标名在执行前都不存在。
func Break(cyc []Step, tmp string) []Step {
	steps := make([]Step, 0, len(cyc)+1)
	steps = append(steps, Step{Old: cyc[0].Old, New: tmp})
	for i := len(cyc) - 1; i >= 1; i-- {
		steps = append(steps, cyc[i])
	}
	steps = append(steps, Step{Old: tmp, New: cyc[0].New})
	return steps
}
