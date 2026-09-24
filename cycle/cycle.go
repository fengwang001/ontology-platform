// Package cycle 检测纯重命名图中的简单环，并用每个环一个临时名将其破解。
package cycle

import (
	"errors"
	"sort"

	"ontology/plan"
)

// ErrTempExhausted 表示所有候选临时名都被占用，无法安全破环（绝不覆盖数据）。
var ErrTempExhausted = errors.New("cycle: all candidate temporary names are occupied")

// DefaultPrefix 是临时名候选前缀。
const DefaultPrefix = ".rename-tmp-"

// TempName 生成一个既不在 existing 中、也不在 reserved（请求新名）中的临时名。
// 冲突时递增后缀重试；attempts 为尝试上限。
func TempName(existing map[string]bool, reserved map[string]bool, prefix string, attempts int) (string, error) {
	if attempts <= 0 {
		attempts = 1 << 20
	}
	for i := 0; i < attempts; i++ {
		cand := prefix + itoa(i)
		if !existing[cand] && !reserved[cand] {
			return cand, nil
		}
	}
	return "", ErrTempExhausted
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

// FindCycles 在 old→new 映射（每个键/值至多出现一次）中找出全部简单环。
// 每个环以字典序最小顶点为首，按 v0,v1,… 返回 v0→v1→…→v{k-1}→v0；
// 环集合本身按首名字典序排列，保证确定性。
func FindCycles(next map[string]string) [][]string {
	seen := make(map[string]bool)
	var cycles [][]string
	starts := make([]string, 0, len(next))
	for v := range next {
		starts = append(starts, v)
	}
	sort.Strings(starts)
	for _, start := range starts {
		if seen[start] {
			continue
		}
		path := []string{}
		pos := make(map[string]int)
		v := start
		for {
			if idx, ok := pos[v]; ok && !seen[v] {
				ring := append([]string(nil), path[idx:]...)
				ring = rotateMin(ring)
				cycles = append(cycles, ring)
				for _, x := range ring {
					seen[x] = true
				}
				break
			}
			if seen[v] {
				break
			}
			pos[v] = len(path)
			path = append(path, v)
			w, ok := next[v]
			if !ok {
				break
			}
			v = w
		}
		for _, x := range path {
			seen[x] = true
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })
	return cycles
}

func rotateMin(ring []string) []string {
	min := 0
	for i := 1; i < len(ring); i++ {
		if ring[i] < ring[min] {
			min = i
		}
	}
	out := make([]string, 0, len(ring))
	out = append(out, ring[min:]...)
	out = append(out, ring[:min]...)
	return out
}

// Break 返回破解环 v0→v1→…→v{k-1}→v0 的步骤：
// v0→t, v{k-1}→v0, v{k-2}→v{k-1}, …, v1→v2, t→v1。
// 每一步的目标名在执行瞬间都不存在。
func Break(ring []string, temp string) []plan.Step {
	k := len(ring)
	steps := []plan.Step{{ring[0], temp}}
	for i := k - 1; i >= 1; i-- {
		steps = append(steps, plan.Step{ring[i], ring[(i+1)%k]})
	}
	steps = append(steps, plan.Step{temp, ring[1]})
	return steps
}
