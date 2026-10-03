package scan

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// 2000 组随机操作序列：引擎 vs 朴素模型，逐操作比对错误与结果，并周期性全量比对
// T/a/epoch/Suspect/会话状态。-v 日志打印每组输入、输出与判定依据。

type fuzzState struct {
	e       *Engine
	m       *naiveModel
	p, k, x int
	open    map[int]bool
	openEp  map[int]int
}

func fuzzNew(p, k, x int) (*fuzzState, error) {
	e, err := New(p, k, x)
	if err != nil {
		return nil, err
	}
	return &fuzzState{e: e, m: newNaive(p, k, x), p: p, k: k, x: x,
		open: map[int]bool{}, openEp: map[int]int{}}, nil
}

func (f *fuzzState) randomRows(rng *rand.Rand, part int) []naiveRow {
	n := rng.Intn(5)
	var rows []naiveRow
	used := map[int64]bool{}
	for i := 0; i < n; i++ {
		k := int64(part) + rng.Int63n(12)*int64(f.p)
		if k < 1 {
			k = int64(part) + 1
		}
		if used[k] {
			continue
		}
		used[k] = true
		rows = append(rows, naiveRow{k: k, v: rng.Int63n(2_000_000_001) - 1_000_000_000})
	}
	return rows
}

func (f *fuzzState) verify(t *testing.T, seed, step int64) {
	t.Helper()
	snap := f.e.snapshot()
	// maxNow
	if snap.maxNow != f.m.maxNow {
		t.Fatalf("seed=%d step=%d maxNow engine=%d naive=%d", seed, step, snap.maxNow, f.m.maxNow)
	}
	for part := 0; part < f.p; part++ {
		if snap.epoch[part] != f.m.epoch[part] {
			t.Fatalf("seed=%d part=%d epoch engine=%d naive=%d", seed, part, snap.epoch[part], f.m.epoch[part])
		}
		if snap.suspect[part] != f.m.suspect[part] {
			t.Fatalf("seed=%d part=%d suspect engine=%v naive=%v", seed, part, snap.suspect[part], f.m.suspect[part])
		}
		if snap.open[part] != f.m.open[part] {
			t.Fatalf("seed=%d part=%d open engine=%v naive=%v", seed, part, snap.open[part], f.m.open[part])
		}
		// T 与 a
		wantT := map[int64]int64{}
		wantA := map[int64]int{}
		for k, v := range f.m.t {
			if int(k%int64(f.p)) == part {
				wantT[k] = v
				wantA[k] = f.m.a[k]
			}
		}
		if len(snap.live[part]) != len(wantT) {
			t.Fatalf("seed=%d part=%d T size engine=%d naive=%d (%v vs %v)",
				seed, part, len(snap.live[part]), len(wantT), snap.live[part], wantT)
		}
		for k, v := range wantT {
			if snap.live[part][k] != v {
				t.Fatalf("seed=%d part=%d T[%d] engine=%v naive=%d", seed, part, k, snap.live[part][k], v)
			}
			if snap.absent[part][k] != wantA[k] {
				t.Fatalf("seed=%d part=%d a[%d] engine=%d naive=%d", seed, part, k, snap.absent[part][k], wantA[k])
			}
		}
	}
}

func TestFuzzAgainstNaive2000(t *testing.T) {
	fuzzVerbose := flag.Lookup("test.v") != nil && flag.Lookup("test.v").Value.String() == "true"
	const groups = 2000
	for seed := int64(1); seed <= groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		p := 1 + rng.Intn(4) // 1..4，保持小规模便于对拍
		k := 1 + rng.Intn(3) // 1..3
		x := 1 + rng.Intn(100)
		f, err := fuzzNew(p, k, x)
		if err != nil {
			t.Fatalf("New(%d,%d,%d): %v", p, k, x, err)
		}
		var logb strings.Builder
		fmt.Fprintf(&logb, "\n=== seed=%d P=%d K=%d X=%d ===", seed, p, k, x)
		steps := 20 + rng.Intn(30)
		now := int64(0)
		for step := 0; step < steps; step++ {
			part := rng.Intn(p)
			now += int64(rng.Intn(2)) // 非降时间戳（多数增长，偶有相等）
			kind := rng.Intn(10)
			switch {
			case kind < 3 || !f.open[part]:
				// Begin
				o := naiveOp{kind: "begin", part: part, now: now}
				ep, nerr := f.m.Begin(o)
				epE, eerr := f.e.Begin(part, now)
				fmt.Fprintf(&logb, "\n[%02d] Begin(part=%d now=%d) -> engine(ep=%d err=%v) naive(ep=%d err=%v)",
					step, part, now, epE, eerr, ep, nerr)
				if sameErr(eerr, nerr) == false {
					t.Fatalf("seed=%d Begin err mismatch engine=%v naive=%v log=%s", seed, eerr, nerr, logb.String())
				}
				if eerr == nil {
					if epE != ep {
						t.Fatalf("seed=%d Begin epoch mismatch %d vs %d", seed, epE, ep)
					}
					f.open[part] = true
					f.openEp[part] = ep
				}
			case kind >= 3 && kind < 8:
				// Report
				rows := f.randomRows(rng, part)
				erows := make([]Row, len(rows))
				for i, r := range rows {
					erows[i] = Row{K: r.k, V: r.v}
				}
				useEp := f.openEp[part]
				if rng.Intn(5) == 0 {
					useEp = f.openEp[part] + 1 + rng.Intn(2) // 偶发错误 epoch
				}
				o := naiveOp{kind: "report", part: part, epoch: useEp, rows: rows, now: now}
				eo, nerr := f.m.Report(o)
				ee, eerr := f.e.Report(part, useEp, erows, now)
				fmt.Fprintf(&logb, "\n[%02d] Report(part=%d ep=%d now=%d rows=%v) -> engine(%v %v) naive(%v %v)",
					step, part, useEp, now, rows, ee, eerr, eo, nerr)
				if !sameErr(eerr, nerr) {
					t.Fatalf("seed=%d Report err mismatch engine=%v naive=%v log=%s", seed, eerr, nerr, logb.String())
				}
				if eerr == nil {
					if len(ee) != len(eo) {
						t.Fatalf("seed=%d Report outcomes len mismatch", seed)
					}
					for i := range ee {
						if ee[i] != WriteOutcome(eo[i]) {
							t.Fatalf("seed=%d Report outcome[%d] engine=%d naive=%d", seed, i, ee[i], eo[i])
						}
					}
				}
			default:
				// End
				complete := rng.Intn(10) != 0
				useEp := f.openEp[part]
				if rng.Intn(5) == 0 {
					useEp = f.openEp[part] + 1
				}
				o := naiveOp{kind: "end", part: part, epoch: useEp, complete: complete, now: now}
				ne, nerr := f.m.End(o)
				ee, eerr := f.e.End(part, useEp, complete, now)
				reason := ""
				if complete && eerr == nil {
					// 判定依据：K 连续缺席、|C|*100 vs X*n0；n0 在 End 前确定，
					// 这里记录操作后存活数与两侧删除/熔断结论用于对账。
					n0After := f.e.testLive(part)
					reason = fmt.Sprintf(" complete=%v tripped=%v deletedE=%d deletedN=%d (live after op=%d)",
						complete, ee.Tripped, ee.Deleted, ne.deleted, n0After)
				}
				fmt.Fprintf(&logb, "\n[%02d] End(part=%d ep=%d complete=%v now=%d) -> engine(%+v %v) naive(%+v %v)%s",
					step, part, useEp, complete, now, ee, eerr, ne, nerr, reason)
				if !sameErr(eerr, nerr) {
					t.Fatalf("seed=%d End err mismatch engine=%v naive=%v log=%s", seed, eerr, nerr, logb.String())
				}
				if eerr == nil {
					if ee.Deleted != ne.deleted || ee.Tripped != ne.tripped {
						t.Fatalf("seed=%d End result mismatch engine=%+v naive=%+v log=%s", seed, ee, ne, logb.String())
					}
					f.open[part] = false
				}
			}

			// 偶发 Approve
			if rng.Intn(6) == 0 {
				part2 := rng.Intn(p)
				role := 2
				if rng.Intn(4) == 0 {
					role = rng.Intn(3) // 0,1,2 中部分非 2
					if role == 2 {
						role = 3
					}
				}
				o := naiveOp{kind: "approve", part: part2, role: role, now: now}
				nd, nerr := f.m.Approve(o)
				ed, eerr := f.e.Approve(role, part2, now)
				fmt.Fprintf(&logb, "\n[%02d] Approve(role=%d part=%d now=%d) -> engine(d=%d err=%v) naive(d=%d err=%v)",
					step, role, part2, now, ed, eerr, nd, nerr)
				if !sameErr(eerr, nerr) || ed != nd {
					t.Fatalf("seed=%d Approve mismatch engine=(%d,%v) naive=(%d,%v) log=%s",
						seed, ed, eerr, nd, nerr, logb.String())
				}
			}

			// now 回退注入（被拒，不改状态）
			if rng.Intn(12) == 0 {
				badNow := now - int64(1+rng.Intn(3))
				if badNow < 0 {
					badNow = 0
				}
				_, eerr := f.e.Begin(part, badNow)
				_, nerr := f.m.Begin(naiveOp{kind: "begin", part: part, now: badNow})
				fmt.Fprintf(&logb, "\n[%02d] rollback-inject Begin(part=%d now=%d) -> engine=%v naive=%v",
					step, part, badNow, eerr, nerr)
				if !sameErr(eerr, nerr) {
					t.Fatalf("seed=%d rollback err mismatch engine=%v naive=%v", seed, eerr, nerr)
				}
			}

			if step%7 == 6 {
				f.verify(t, seed, int64(step))
			}
		}
		f.verify(t, seed, int64(steps))
		// 默认每组一行摘要；-v 时额外打印逐步输入/输出/判定依据明细。
		t.Logf("seed=%d P=%d K=%d X=%d steps=%d OK", seed, p, k, x, steps)
		if fuzzVerbose {
			t.Log(strings.TrimSpace(logb.String()))
		}
	}
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error())
}

var _ = sort.Ints
