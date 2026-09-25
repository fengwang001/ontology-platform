// Package hunk 把编辑脚本按上下文行数分组成 hunk。
package hunk

import "ontology/edit"

// Hunk 是一段改动连同上下各 ctx 行上下文。
// A0/B0 为 0 基起始下标，AC/BC 为两侧行数（含上下文）。
type Hunk struct {
	A0, B0 int
	AC, BC int
	Ops    []edit.Op
}

// Group 把脚本 s 按上下文行数 ctx 分组。两段改动之间的未改动行数
// g <= 2*ctx 时合并为一个 hunk，否则分开（推导见 DESIGN.md 第 2 节）。
func Group(s edit.Script, ctx int) []Hunk {
	n := len(s)
	pa := make([]int, n+1) // 每个操作之前的 a 侧下标
	pb := make([]int, n+1)
	for i, op := range s {
		pa[i+1], pb[i+1] = pa[i], pb[i]
		switch op {
		case edit.Keep:
			pa[i+1]++
			pb[i+1]++
		case edit.Del:
			pa[i+1]++
		case edit.Ins:
			pb[i+1]++
		}
	}
	isChg := make([]bool, n)
	first, last := -1, -1
	for i, op := range s {
		if op != edit.Keep {
			isChg[i] = true
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil
	}
	var hs []Hunk
	for lo := first; lo <= last; {
		hi := lo // [lo,hi] 为当前合并段内的改动下标范围
		for j := lo + 1; j <= last; j++ {
			if !isChg[j] {
				continue
			}
			if j-hi-1 <= 2*ctx { // 间隔 g = j-hi-1
				hi = j
			} else {
				break
			}
		}
		from := lo - ctx
		if from < 0 {
			from = 0
		}
		to := hi + 1 + ctx
		if to > n {
			to = n
		}
		hs = append(hs, Hunk{
			A0: pa[from], B0: pb[from],
			AC: pa[to] - pa[from], BC: pb[to] - pb[from],
			Ops: s[from:to],
		})
		lo = hi + 1
		for lo <= last && !isChg[lo] {
			lo++
		}
	}
	return hs
}
