package redo

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveReplay 是按规格逐步写成的朴素顺序回放器：
// 已 End 的小事务记录严格按 LSN 升序逐条处理，File 立即执行，Finish 丢弃暂存。
type naiveReplay struct {
	spaces    map[int]map[int]naivePage
	staged    []Record
	applied   int
	skipped   int
	missing   int
	discarded int
}

type naivePage struct {
	value int64
	lsn   int64
}

func newNaive() *naiveReplay {
	return &naiveReplay{spaces: make(map[int]map[int]naivePage)}
}

func (n *naiveReplay) load(space, page int, value, lsn int64) {
	pages := n.spaces[space]
	if pages == nil {
		pages = make(map[int]naivePage)
		n.spaces[space] = pages
	}
	pages[page] = naivePage{value: value, lsn: lsn}
}

func (n *naiveReplay) feed(rec Record) {
	switch rec.Type {
	case PageRec:
		n.staged = append(n.staged, rec)
	case EndRec:
		for _, s := range n.staged {
			n.apply(s)
		}
		n.staged = n.staged[:0]
	case FileRec:
		if rec.Kind == Create {
			if _, ok := n.spaces[rec.Space]; !ok {
				n.spaces[rec.Space] = make(map[int]naivePage)
			}
		} else {
			delete(n.spaces, rec.Space)
		}
	}
}

func (n *naiveReplay) apply(rec Record) {
	pages, ok := n.spaces[rec.Space]
	if !ok {
		n.missing++
		return
	}
	p := pages[rec.Page]
	if rec.LSN <= p.lsn {
		n.skipped++
		return
	}
	p.value += rec.Delta
	p.lsn = rec.LSN
	pages[rec.Page] = p
	n.applied++
}

func (n *naiveReplay) finish() {
	n.discarded += len(n.staged)
	n.staged = nil
}

func (n *naiveReplay) state(space, page int) (int64, int64) {
	if pages, ok := n.spaces[space]; ok {
		if p, ok := pages[page]; ok {
			return p.value, p.lsn
		}
	}
	return 0, 0
}

type pageKey struct {
	space int
	page  int
}

type pageVal struct {
	value int64
	lsn   int64
}

// genLog 生成一组随机加载与随机日志（含随机截断：末尾可能留未 End 的小事务）。
func genLog(rnd *rand.Rand) (loads [][4]int64, recs []Record) {
	for i, n := 0, rnd.Intn(5); i < n; i++ {
		loads = append(loads, [4]int64{
			int64(rnd.Intn(5)), int64(rnd.Intn(6)),
			int64(rnd.Intn(41) - 20), int64(rnd.Intn(11)),
		})
	}
	var lsn int64
	mtr := 0
	open := false
	truncate := rnd.Intn(5) == 0
	for i, n := 0, rnd.Intn(61); i < n; i++ {
		if open {
			if rnd.Intn(3) > 0 {
				lsn++
				recs = append(recs, PageRecord(lsn, mtr, rnd.Intn(5), rnd.Intn(6), int64(rnd.Intn(41)-20)))
			} else {
				lsn++
				recs = append(recs, EndRecord(lsn, mtr))
				open = false
			}
			continue
		}
		mtr++
		if rnd.Intn(4) == 0 {
			kind := Create
			if rnd.Intn(2) == 0 {
				kind = Delete
			}
			lsn++
			recs = append(recs, FileRecord(lsn, mtr, kind, rnd.Intn(5)))
		} else {
			lsn++
			recs = append(recs, PageRecord(lsn, mtr, rnd.Intn(5), rnd.Intn(6), int64(rnd.Intn(41)-20)))
			open = true
		}
	}
	if open && !truncate {
		lsn++
		recs = append(recs, EndRecord(lsn, mtr))
	}
	return loads, recs
}

// runReplay 用给定 W、M 回放同一输入，返回最终页状态、统计与 ApplyLog。
func runReplay(t *testing.T, w, m int, loads [][4]int64, recs []Record, keys []pageKey) (map[pageKey]pageVal, Stats, []ApplyEntry) {
	t.Helper()
	r, err := NewReplayer(w, m)
	if err != nil {
		t.Fatalf("NewReplayer(%d,%d) 失败: %v", w, m, err)
	}
	for _, l := range loads {
		if err := r.LoadPage(int(l[0]), int(l[1]), l[2], l[3]); err != nil {
			t.Fatalf("LoadPage(%v) 失败: %v", l, err)
		}
	}
	for _, rec := range recs {
		if err := r.Feed(rec); err != nil {
			t.Fatalf("Feed(%+v) 失败: %v", rec, err)
		}
		// 不变量：已接受 Page 记录数 = 应用+已应用+空间缺失+被丢弃+暂存或排队中的数。
		s := r.Stats()
		if s.Accepted != s.Applied+s.Skipped+s.Missing+s.Discarded+s.Pending+s.Staged {
			t.Fatalf("Feed(%+v) 后不变量被破坏: %+v", rec, s)
		}
	}
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	states := make(map[pageKey]pageVal, len(keys))
	for _, k := range keys {
		v, l := r.PageState(k.space, k.page)
		states[k] = pageVal{value: v, lsn: l}
	}
	return states, r.Stats(), r.ApplyLog()
}

// 2000 组随机日志（含随机截断）与朴素顺序回放对照，并验证确定性与 W/M 无关性。
func TestRandomLogsAgainstNaive(t *testing.T) {
	const cases = 2000
	for i := 0; i < cases; i++ {
		rnd := rand.New(rand.NewSource(int64(i)*2654435761 + 1))
		w := 1 + rnd.Intn(MaxWorkers)
		m := 1 + rnd.Intn(20)
		if rnd.Intn(4) == 0 {
			m = 1 + rnd.Intn(MaxBatch)
		}
		loads, recs := genLog(rnd)

		keySet := make(map[pageKey]bool)
		for _, l := range loads {
			keySet[pageKey{int(l[0]), int(l[1])}] = true
		}
		for _, rec := range recs {
			if rec.Type == PageRec {
				keySet[pageKey{rec.Space, rec.Page}] = true
			}
		}
		var keys []pageKey
		for k := range keySet {
			keys = append(keys, k)
		}

		// 朴素顺序回放（判定依据）。
		n := newNaive()
		for _, l := range loads {
			n.load(int(l[0]), int(l[1]), l[2], l[3])
		}
		for _, rec := range recs {
			n.feed(rec)
		}
		n.finish()
		wantStates := make(map[pageKey]pageVal, len(keys))
		for _, k := range keys {
			v, l := n.state(k.space, k.page)
			wantStates[k] = pageVal{value: v, lsn: l}
		}

		states, stats, applyLog := runReplay(t, w, m, loads, recs, keys)

		// 相同输入重放得到完全相同的批次与 ApplyLog。
		states2, stats2, applyLog2 := runReplay(t, w, m, loads, recs, keys)
		if !reflect.DeepEqual(applyLog, applyLog2) || stats.Batches != stats2.Batches {
			t.Fatalf("case %d: 相同输入重放的批次或 ApplyLog 不同", i)
		}
		if !reflect.DeepEqual(states, states2) {
			t.Fatalf("case %d: 相同输入重放的最终页状态不同", i)
		}

		// 与 W、M 取值无关：W=1/M=1 与大 W/大 M 的最终状态一致。
		for _, wm := range [][2]int{{1, 1}, {MaxWorkers, MaxBatch}} {
			s, st, _ := runReplay(t, wm[0], wm[1], loads, recs, keys)
			if !reflect.DeepEqual(s, states) {
				t.Fatalf("case %d: W=%d,M=%d 的最终状态与 W=%d,M=%d 不同", i, wm[0], wm[1], w, m)
			}
			if st.Applied != stats.Applied || st.Skipped != stats.Skipped ||
				st.Missing != stats.Missing || st.Discarded != stats.Discarded {
				t.Fatalf("case %d: W=%d,M=%d 的计数与 W=%d,M=%d 不同", i, wm[0], wm[1], w, m)
			}
		}

		// 最终页状态与朴素顺序回放一致。
		if !reflect.DeepEqual(states, wantStates) {
			t.Fatalf("case %d: 最终页状态与朴素顺序回放不一致\n输入 loads=%v recs=%v\n got=%v\nwant=%v",
				i, loads, recs, states, wantStates)
		}
		if stats.Applied != n.applied || stats.Skipped != n.skipped ||
			stats.Missing != n.missing || stats.Discarded != n.discarded {
			t.Fatalf("case %d: 计数与朴素顺序回放不一致\n输入 loads=%v recs=%v\n got=%+v\nwant=%+v",
				i, loads, recs, stats, n)
		}

		t.Logf("case %d 输入: W=%d M=%d loads=%v recs=%v", i, w, m, fmtLoads(loads), recs)
		t.Logf("case %d 输出: states=%v stats=%+v", i, states, stats)
		t.Logf("case %d 判定依据: 与按 LSN 升序逐条处理的朴素顺序回放逐键比较一致，且重放确定、与 W/M 无关", i)
	}
}

func fmtLoads(loads [][4]int64) string {
	s := "["
	for i, l := range loads {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("(space=%d,page=%d,value=%d,lsn=%d)", l[0], l[1], l[2], l[3])
	}
	return s + "]"
}
