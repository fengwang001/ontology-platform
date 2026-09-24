// Package stat 计算各租户的执行代价份额与相对理论权重占比的偏差。
package stat

import "sort"

// Share 是单个租户的份额度量。
type Share struct {
	// Tenant 为租户 ID。
	Tenant string
	// Actual 为实际执行代价占比：cost_i / Σcost。
	Actual float64
	// Expected 为理论权重占比：w_i / Σw。
	Expected float64
	// Dev 为相对偏差：|Actual-Expected|/Expected。
	Dev float64
}

// Report 根据各租户权重与累计执行代价计算份额报告，
// 按租户 ID 字典序返回，结果可复现。总权重或总代价为 0 时对应项为 0。
func Report(weights map[string]float64, cost map[string]float64) []Share {
	var sumW, sumC float64
	for _, w := range weights {
		sumW += w
	}
	for _, c := range cost {
		sumC += c
	}
	out := make([]Share, 0, len(weights))
	for id, w := range weights {
		sh := Share{Tenant: id}
		if sumW > 0 {
			sh.Expected = w / sumW
		}
		if sumC > 0 {
			sh.Actual = cost[id] / sumC
		}
		if sh.Expected > 0 {
			d := sh.Actual - sh.Expected
			if d < 0 {
				d = -d
			}
			sh.Dev = d / sh.Expected
		}
		out = append(out, sh)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tenant < out[j].Tenant })
	return out
}

// MaxDev 返回报告中最大的相对偏差。
func MaxDev(rep []Share) float64 {
	m := 0.0
	for _, sh := range rep {
		if sh.Dev > m {
			m = sh.Dev
		}
	}
	return m
}
