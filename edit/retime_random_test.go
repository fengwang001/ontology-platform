package edit_test

import (
	"math/rand"
	"testing"

	"ontology/cue"
	"ontology/edit"
)

// naiveRetime 用逐毫秒建立新旧时间线对应表的朴素模拟重定时一条字幕轨。
// oldToNew[i] 为旧毫秒 [i,i+1) 在新时间线中的起点位置（插入段产生的新
// 毫秒标记为 -1）。
func naiveRetime(tbl edit.Table, cues []cue.Cue, dmin, horizon int64) []cue.Cue {
	// 标注删除毫秒。
	deleted := make([]bool, horizon+1)
	for _, d := range tbl.Deletes {
		for t := d.A; t < d.B; t++ {
			deleted[t] = true
		}
	}
	// 标注插入毫秒（at 是旧时间线坐标：插入新画面恰在旧坐标 at 之前）。
	inserted := map[int64]int64{} // at -> 插入毫秒数
	for _, in := range tbl.Inserts {
		inserted[in.At] += in.Len
	}
	// 扫描旧坐标：坐标 i 处先放插入块，再放旧毫秒 i（若存活）。
	// oldStart[i] 为旧毫秒 [i,i+1) 在新时间线上的起点，删除毫秒为 -1。
	var cur int64
	oldStart := make([]int64, horizon)
	for i := int64(0); i < horizon; i++ {
		cur += inserted[i]
		if deleted[i] {
			oldStart[i] = -1
			continue
		}
		oldStart[i] = cur
		cur++
	}
	var out []cue.Cue
	for _, cu := range cues {
		// 按内部插入点切片；每片存活旧毫秒在新时间线上连续（删洞不切片）。
		piece := func(p, q int64) {
			var ns, ne int64 = -1, -1
			for i := p; i < q; i++ {
				if oldStart[i] < 0 {
					continue
				}
				if ns < 0 {
					ns = oldStart[i]
				}
				ne = oldStart[i] + 1
			}
			if ns >= 0 && ne-ns >= dmin {
				out = append(out, cue.Cue{Start: ns, End: ne, Text: cu.Text})
			}
		}
		cuts := []int64{}
		for _, in := range tbl.Inserts {
			if cu.Start < in.At && in.At < cu.End {
				cuts = append(cuts, in.At)
			}
		}
		prev := cu.Start
		for _, at := range cuts {
			piece(prev, at)
			prev = at
		}
		piece(prev, cu.End)
	}
	return out
}

func randomTable(r *rand.Rand, horizon int64) edit.Table {
	tbl := edit.Table{}
	nDel := r.Intn(5)
	nIns := r.Intn(5)
	usedDel := map[[2]int64]bool{}
	for i := 0; i < nDel; i++ {
		a := r.Int63n(horizon)
		b := a + 1 + r.Int63n(horizon-a)
		if usedDel[[2]int64{a, b}] {
			continue
		}
		usedDel[[2]int64{a, b}] = true
		tbl.Deletes = append(tbl.Deletes, edit.Delete{A: a, B: b})
	}
	// 排序删除段并重采样重叠。
	sortDeletes(tbl.Deletes)
	filtered := tbl.Deletes[:0]
	for _, d := range tbl.Deletes {
		if len(filtered) > 0 && d.A < filtered[len(filtered)-1].B {
			continue
		}
		filtered = append(filtered, d)
	}
	tbl.Deletes = filtered
	atUsed := map[int64]bool{}
	for i := 0; i < nIns; i++ {
		at := r.Int63n(horizon + 1)
		if atUsed[at] {
			continue
		}
		// 避开严格落在删除内部。
		inside := false
		for _, d := range tbl.Deletes {
			if d.A < at && at < d.B {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		atUsed[at] = true
		tbl.Inserts = append(tbl.Inserts, edit.Insert{At: at, Len: 1 + r.Int63n(4)})
	}
	sortInserts(tbl.Inserts)
	return tbl
}

func sortDeletes(ds []edit.Delete) {
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j-1].A > ds[j].A; j-- {
			ds[j-1], ds[j] = ds[j], ds[j-1]
		}
	}
}

func sortInserts(is []edit.Insert) {
	for i := 1; i < len(is); i++ {
		for j := i; j > 0 && is[j-1].At > is[j].At; j-- {
			is[j-1], is[j] = is[j], is[j-1]
		}
	}
}

func randomCues(r *rand.Rand, horizon, dmin int64) []cue.Cue {
	n := r.Intn(5)
	var cs []cue.Cue
	var cursor int64
	for i := 0; i < n; i++ {
		s := cursor + r.Int63n(6)
		if s >= horizon {
			break
		}
		maxLen := horizon - s
		if maxLen > 12 {
			maxLen = 12
		}
		e := s + dmin + r.Int63n(maxLen+1) - r.Int63n(3)
		if e <= s || e > horizon {
			e = s + dmin
			if e > horizon {
				break
			}
		}
		// 允许故意构造过短字幕以测试输入侧（重定时不校验输入合法性，
		// 但输出仍以 dmin 过滤）。
		cs = append(cs, cue.Cue{Start: s, End: e, Text: "t"})
		cursor = e
	}
	return cs
}

func TestRetimeVsNaive1500(t *testing.T) {
	if !testing.Verbose() {
		t.Log("rerun with -v to dump every case")
	}
	r := rand.New(rand.NewSource(20261005))
	const horizon int64 = 60
	const cases = 1500
	for it := 0; it < cases; it++ {
		dmin := int64(1 + r.Intn(5))
		tbl := randomTable(r, horizon)
		cues := randomCues(r, horizon, 1) // 输入字幕可短，输出以 dmin 过滤
		compiled, err := edit.Compile(tbl)
		if err != nil {
			t.Fatalf("case %d compile: %v table=%+v", it, err, tbl)
		}
		got := edit.Retime(compiled, cues, dmin).Cues
		want := naiveRetime(tbl, cues, dmin, horizon)
		if len(got) != len(want) {
			t.Fatalf("case %d len mismatch\ntable=%+v dmin=%d\nin=%+v\ngot =%+v\nwant=%+v",
				it, tbl, dmin, cues, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("case %d piece %d mismatch\ntable=%+v dmin=%d\nin=%+v\ngot =%+v\nwant=%+v",
					it, i, tbl, dmin, cues, got, want)
			}
		}
		if testing.Verbose() && (it < 10 || it%100 == 0) {
			t.Logf("case %d OK: dmin=%d table=%+v in=%+v -> %+v", it, dmin, tbl, cues, got)
		}
	}
}
