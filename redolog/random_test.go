package redolog

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naiveReplayer 是严格按 LSN 升序逐条处理全部已 End 小事务记录的朴素顺序回放器，
// 作为并行回放器的对照基准。输入假定全部合法（由生成器保证）。
type naiveReplayer struct {
	spaces  map[int64]map[int64]pageState
	results map[int64]ApplyResult
	staged  []pageRec
}

func newNaive() *naiveReplayer {
	return &naiveReplayer{
		spaces:  make(map[int64]map[int64]pageState),
		results: make(map[int64]ApplyResult),
	}
}

func (n *naiveReplayer) load(space, pg, value, lsn int64) {
	sp, ok := n.spaces[space]
	if !ok {
		sp = make(map[int64]pageState)
		n.spaces[space] = sp
	}
	sp[pg] = pageState{value: value, lsn: lsn}
}

func (n *naiveReplayer) applyOne(rec pageRec) {
	sp, ok := n.spaces[rec.space]
	if !ok {
		n.results[rec.lsn] = SpaceMissing
		return
	}
	p := sp[rec.page]
	if rec.lsn <= p.lsn {
		n.results[rec.lsn] = AlreadyApplied
		return
	}
	p.value += rec.delta
	p.lsn = rec.lsn
	sp[rec.page] = p
	n.results[rec.lsn] = Applied
}

func (n *naiveReplayer) feed(rec Record) {
	switch rec.Type {
	case RecPage:
		n.staged = append(n.staged, pageRec{lsn: rec.LSN, space: rec.Space, page: rec.Page, delta: rec.Delta})
	case RecEnd:
		for _, pr := range n.staged {
			n.applyOne(pr)
		}
		n.staged = n.staged[:0]
	case RecFile:
		if rec.Kind == FileCreate {
			if _, ok := n.spaces[rec.Space]; !ok {
				n.spaces[rec.Space] = make(map[int64]pageState)
			}
		} else {
			delete(n.spaces, rec.Space)
		}
	}
}

func (n *naiveReplayer) finish() {
	n.staged = nil
}

type loadRec struct {
	space, page, value, lsn int64
}

type genLog struct {
	loads []loadRec
	recs  []Record
}

// genRandomLog 生成合法日志：mtr 号严格递增、记录连续、File 自成小事务，
// 并以约 1/3 概率随机截断（末尾小事务缺 End）。
func genRandomLog(rng *rand.Rand) genLog {
	var g genLog
	nLoads := rng.Intn(5)
	for i := 0; i < nLoads; i++ {
		g.loads = append(g.loads, loadRec{
			space: int64(rng.Intn(4)),
			page:  int64(rng.Intn(6)),
			value: int64(rng.Intn(101) - 50),
			lsn:   int64(rng.Intn(11)),
		})
	}
	lsn := int64(0)
	mtr := int64(0)
	nextLSN := func() int64 {
		lsn += 1 + int64(rng.Intn(3))
		return lsn
	}
	nEvents := 1 + rng.Intn(30)
	for i := 0; i < nEvents; i++ {
		if rng.Intn(5) == 0 {
			mtr++
			kind := FileCreate
			if rng.Intn(2) == 0 {
				kind = FileDelete
			}
			g.recs = append(g.recs, file(nextLSN(), mtr, kind, int64(rng.Intn(4))))
			continue
		}
		mtr++
		k := 1 + rng.Intn(4)
		for j := 0; j < k; j++ {
			g.recs = append(g.recs, page(nextLSN(), mtr, int64(rng.Intn(4)), int64(rng.Intn(6)), int64(rng.Intn(41)-20)))
		}
		truncated := i == nEvents-1 && rng.Intn(3) == 0
		if !truncated {
			g.recs = append(g.recs, end(nextLSN(), mtr))
		}
	}
	return g
}

func describeLog(g genLog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "loads=%v recs=[", g.loads)
	for _, rec := range g.recs {
		switch rec.Type {
		case RecPage:
			fmt.Fprintf(&b, " P(lsn=%d,mtr=%d,s=%d,p=%d,d=%d)", rec.LSN, rec.Mtr, rec.Space, rec.Page, rec.Delta)
		case RecFile:
			k := "Create"
			if rec.Kind == FileDelete {
				k = "Delete"
			}
			fmt.Fprintf(&b, " F(lsn=%d,mtr=%d,%s,s=%d)", rec.LSN, rec.Mtr, k, rec.Space)
		case RecEnd:
			fmt.Fprintf(&b, " E(lsn=%d,mtr=%d)", rec.LSN, rec.Mtr)
		}
	}
	b.WriteString(" ]")
	return b.String()
}

func digestSpaces(spaces map[int64]map[int64]pageState) string {
	var b strings.Builder
	keys := make([]int64, 0, len(spaces))
	for s := range spaces {
		keys = append(keys, s)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, s := range keys {
		pages := spaces[s]
		pks := make([]int64, 0, len(pages))
		for p := range pages {
			pks = append(pks, p)
		}
		sort.Slice(pks, func(i, j int) bool { return pks[i] < pks[j] })
		fmt.Fprintf(&b, "s%d:{", s)
		for _, p := range pks {
			fmt.Fprintf(&b, " p%d=(v=%d,lsn=%d)", p, pages[p].value, pages[p].lsn)
		}
		b.WriteString(" }")
	}
	return b.String()
}

func runParallel(t *testing.T, w, m int, g genLog) *Replayer {
	t.Helper()
	r, err := NewReplayer(w, m)
	if err != nil {
		t.Fatalf("NewReplayer 失败: %v", err)
	}
	for _, ld := range g.loads {
		if err := r.LoadPage(ld.space, ld.page, ld.value, ld.lsn); err != nil {
			t.Fatalf("LoadPage 失败: %v", err)
		}
	}
	for _, rec := range g.recs {
		if err := r.Feed(rec); err != nil {
			t.Fatalf("Feed(%+v) 失败: %v", rec, err)
		}
	}
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	return r
}

func runNaive(g genLog) *naiveReplayer {
	n := newNaive()
	for _, ld := range g.loads {
		n.load(ld.space, ld.page, ld.value, ld.lsn)
	}
	for _, rec := range g.recs {
		n.feed(rec)
	}
	n.finish()
	return n
}

// 与朴素顺序回放对照 2000 组随机日志（含随机截断），
// 日志中打印输入、输出与判定依据。
func TestRandomLogsAgainstNaive(t *testing.T) {
	const cases = 2000
	for i := 0; i < cases; i++ {
		rng := rand.New(rand.NewSource(int64(i) + 1))
		w := 1 + rng.Intn(64)
		m := 1 + rng.Intn(40)
		g := genRandomLog(rng)

		r := runParallel(t, w, m, g)
		n := runNaive(g)

		// 判定依据一：每条已入队记录的处理结果与朴素回放一致（按 lsn 对照）。
		gotResults := make(map[int64]ApplyResult)
		for _, e := range r.ApplyLog() {
			gotResults[e.LSN] = e.Result
		}
		if !reflect.DeepEqual(gotResults, n.results) {
			t.Fatalf("case %d 输入 %s\n并行结果 %v\n朴素结果 %v", i, describeLog(g), gotResults, n.results)
		}
		// 判定依据二：最终页状态与朴素回放完全一致（与 W、M 无关）。
		if !reflect.DeepEqual(r.spaces, n.spaces) {
			t.Fatalf("case %d 输入 %s\n并行终态 %s\n朴素终态 %s",
				i, describeLog(g), digestSpaces(r.spaces), digestSpaces(n.spaces))
		}
		// 判定依据三：计数不变式。
		st := r.Stats()
		if st.AcceptedPages != st.Applied+st.AlreadyApplied+st.SpaceMissing+st.Discarded+st.Buffered {
			t.Fatalf("case %d 计数不变式不成立: %+v", i, st)
		}
		if st.Buffered != 0 {
			t.Fatalf("case %d Finish 后仍有暂存/排队记录: %+v", i, st)
		}
		// 判定依据四：相同输入序列重放得到完全相同的批次与 ApplyLog。
		r2 := runParallel(t, w, m, g)
		if !reflect.DeepEqual(r.ApplyLog(), r2.ApplyLog()) || r.Batches() != r2.Batches() {
			t.Fatalf("case %d 重放不确定", i)
		}

		t.Logf("case %d 输入 W=%d M=%d %s", i, w, m, describeLog(g))
		t.Logf("case %d 输出 batches=%d applyLog=%v 终态=%s", i, r.Batches(), r.ApplyLog(), digestSpaces(r.spaces))
		t.Logf("case %d 判定依据: 结果与终态同朴素顺序回放一致，计数不变式成立，重放确定", i)
	}
}

// W=1 与大 W 的最终状态一致。
func TestW1VsLargeWConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for i := 0; i < 200; i++ {
		g := genRandomLog(rng)
		r1 := runParallel(t, 1, 1+rng.Intn(10), g)
		r64 := runParallel(t, 64, 1+rng.Intn(10), g)
		if !reflect.DeepEqual(r1.spaces, r64.spaces) {
			t.Fatalf("第 %d 组: W=1 与 W=64 终态不一致\nW=1: %s\nW=64: %s",
				i, digestSpaces(r1.spaces), digestSpaces(r64.spaces))
		}
	}
}
