// Package cycle 借助临时名破解环形重命名，并产出最终的线性步骤。
package cycle

import (
	"errors"

	"ontology/plan"
)

// ErrNoTemp 表示无法分配一个不冲突的临时名。
var ErrNoTemp = errors.New("cycle: no available temporary name")

const maxAttempts = 1_000_000_000

// Break 把计划中的每个环展开为「一个临时名 + 三步以上的线性步骤」，
// 并与链步骤拼接成最终执行序列。临时名数量恰等于环的数量。
// reserved 为现有名字与本批所有新名（含链目标）的并集。
func Break(pl *plan.Plan, reserved []string) ([]plan.Step, []string, error) {
	taken := make(map[string]struct{}, len(reserved))
	for _, n := range reserved {
		taken[n] = struct{}{}
	}

	used := make(map[string]struct{}, len(pl.Cycles))
	alloc := func() (string, error) {
		for i := 0; i < maxAttempts; i++ {
			cand := tempName(i)
			if _, a := taken[cand]; a {
				continue
			}
			if _, b := used[cand]; b {
				continue
			}
			used[cand] = struct{}{}
			taken[cand] = struct{}{}
			return cand, nil
		}
		return "", ErrNoTemp
	}

	var temps []string
	var out []plan.Step
	out = append(out, pl.Chains...)
	for _, cyc := range pl.Cycles {
		t, err := alloc()
		if err != nil {
			return nil, nil, err
		}
		temps = append(temps, t)
		out = append(out, expand(cyc.Reqs, t)...)
	}
	return out, temps, nil
}

// expand 将环 [v0→v1, v1→v2, …, v_{n-1}→v0] 展开为：
// v0→t, v_{n-1}→v0, v_{n-2}→v_{n-1}, …, v1→v2, t→v1。
func expand(reqs []plan.Req, t string) []plan.Step {
	n := len(reqs)
	steps := make([]plan.Step, 0, n+1)
	steps = append(steps, plan.Step{reqs[0].From, t})
	for i := n - 1; i >= 1; i-- {
		steps = append(steps, plan.Step{reqs[i].From, reqs[i].To})
	}
	steps = append(steps, plan.Step{t, reqs[0].To})
	return steps
}

func tempName(i int) string {
	return ".rename-tmp-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
