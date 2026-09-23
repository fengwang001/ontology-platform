// Package hunk 把编辑脚本按上下文行数 C 分组为若干 unified-diff hunk。
package hunk

import "ontology/edit"

// Hunk 是一段连续改动（含上下文）。区间为 0 基、半开；
// OldStart 保留“插入点前行数”的原始值（可为 0），与 OldCount=0 配合使用。
type Hunk struct {
	Items             []edit.Item
	OldStart, NewStart int
	OldCount, NewCount int
}

// Build 按上下文行数 context 对脚本分组。两段改动间未改动行 g ≤ 2*context 时合并，
// g ≥ 2*context+1 时分隔（见 DESIGN.md 第 2 节）。
func Build(items []edit.Item, context int) []Hunk {
	type region struct{ o1, o2, n1, n2 int }
	var regions []region
	oi, ni := 0, 0
	for _, it := range items {
		switch it.Op {
		case edit.Equal:
			oi, ni = oi+1, ni+1
		case edit.Delete:
			if len(regions) == 0 || regions[len(regions)-1].o2 < oi {
				regions = append(regions, region{o1: oi, o2: oi, n1: ni, n2: ni})
			}
			regions[len(regions)-1].o2 = oi + 1
			oi++
		case edit.Insert:
			if len(regions) == 0 || regions[len(regions)-1].n2 < ni {
				regions = append(regions, region{o1: oi, o2: oi, n1: ni, n2: ni})
			}
			regions[len(regions)-1].n2 = ni + 1
			ni++
		}
	}
	totalO, totalN := totalOld(items), totalNew(items)
	var groups []region
	for _, r := range regions {
		if len(groups) == 0 || r.o1-groups[len(groups)-1].o2 > 2*context {
			groups = append(groups, r)
			continue
		}
		groups[len(groups)-1].o2 = r.o2
		groups[len(groups)-1].n2 = r.n2
	}
	var hs []Hunk
	for _, r := range groups {
		o1 := r.o1 - min(context, r.o1)
		n1 := r.n1 - min(context, r.n1)
		hs = append(hs, Hunk{
			OldStart:  o1,
			NewStart:  n1,
			OldCount:  r.o2 + min(context, totalO-r.o2) - o1,
			NewCount:  r.n2 + min(context, totalN-r.n2) - n1,
		})
	}
	for i := range hs {
		hs[i].Items = sliceItems(items, hs[i].OldStart, hs[i].OldCount, hs[i].NewStart, hs[i].NewCount)
	}
	return hs
}

func sliceItems(items []edit.Item, o1, oc, n1, nc int) []edit.Item {
	oi, ni := 0, 0
	var out []edit.Item
	for _, it := range items {
		oin, nin := oi, ni
		if it.Op != edit.Insert {
			oin++
		}
		if it.Op != edit.Delete {
			nin++
		}
		if (it.Op == edit.Insert && ni >= n1 && ni < n1+nc) ||
			(it.Op != edit.Insert && oi >= o1 && oi < o1+oc) {
			out = append(out, it)
		}
		oi, ni = oin, nin
	}
	return out
}

func totalOld(items []edit.Item) int {
	n := 0
	for _, it := range items {
		if it.Op != edit.Insert {
			n++
		}
	}
	return n
}

func totalNew(items []edit.Item) int {
	n := 0
	for _, it := range items {
		if it.Op != edit.Delete {
			n++
		}
	}
	return n
}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}
