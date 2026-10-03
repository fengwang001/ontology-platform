package plan

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/diff"
)

func joinLog(log []string) string {
	out := ""
	for _, l := range log {
		out += "\n  " + l
	}
	return out
}

func dumpItems(its []naiveItem) string {
	s := "naive plan:"
	for _, it := range its {
		s += fmt.Sprintf("\n  id=%d kind=%d ver=%d ex=%v cols=%v", it.id, it.kind, it.ver, it.ex, it.cols)
	}
	return s
}

func assertResultsEqual(t *testing.T, g int, log []string, got []diff.Result, want []naiveResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("group %d Compare rows %d want %d%s", g, len(got), len(want), joinLog(log))
	}
	for i := range want {
		a, b := got[i], want[i]
		if a.ID != b.id || a.Kind != b.kind {
			t.Fatalf("group %d row %d: got (id=%d kind=%s) want (id=%d kind=%s)%s",
				g, i, a.ID, kind(a.Kind), b.id, kind(b.kind), joinLog(log))
		}
		if len(a.Cols) != len(b.cols) {
			t.Fatalf("group %d id %d cols %v want %v%s", g, a.ID, a.Cols, b.cols, joinLog(log))
		}
		for j := range b.cols {
			if a.Cols[j] != b.cols[j] {
				t.Fatalf("group %d id %d cols %v want %v%s", g, a.ID, a.Cols, b.cols, joinLog(log))
			}
		}
		if !nDEq(a.SD, b.sd) || !nDEq(a.TD, b.td) || !nCEq(a.SC, b.sc) || !nCEq(a.TC, b.tc) {
			t.Fatalf("group %d id %d normalized values differ%s", g, a.ID, joinLog(log))
		}
		if a.Ver != b.ver || a.Exist != b.ex {
			t.Fatalf("group %d id %d snapshot ver/ex %d/%v want %d/%v%s",
				g, a.ID, a.Ver, a.Exist, b.ver, b.ex, joinLog(log))
		}
	}
}

func assertItemsEqual(t *testing.T, g int, log []string, got []Item, want []naiveItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("group %d plan len %d want %d%s", g, len(got), len(want), joinLog(log))
	}
	for i := range want {
		a, b := got[i], want[i]
		if a.ID != b.id || a.Kind != b.kind || a.Ver != b.ver || a.Exist != b.ex {
			t.Fatalf("group %d item %d header %+v want %+v%s", g, i, a, b, joinLog(log))
		}
		if !nDEq(a.D, b.d) || !nCEq(a.C, b.c) {
			t.Fatalf("group %d item %d values differ%s", g, i, joinLog(log))
		}
		if len(a.Cols) != len(b.cols) {
			t.Fatalf("group %d item %d cols %v want %v%s", g, i, a.Cols, b.cols, joinLog(log))
		}
		for j := range b.cols {
			if a.Cols[j] != b.cols[j] {
				t.Fatalf("group %d item %d cols %v want %v%s", g, i, a.Cols, b.cols, joinLog(log))
			}
		}
	}
}

// TestRandomDifferential 用 2000 组随机表与操作序列对拍产品实现与朴素模拟：
// Compare 分类/不等列/归一值、Plan 内容、Apply 成功计数或 ErrStale 的 id 逐项一致，
// 成功应用后 Compare 必须收敛；每组打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(20261003))
	for g := 0; g < groups; g++ {
		sd, rm, ne := rng.Intn(7), rng.Intn(3), rng.Intn(2) == 1
		eng, err := diff.New(sd, rm, ne)
		if err != nil {
			t.Fatal(err)
		}
		md := naiveNew(sd, rm, ne)
		log := []string{fmt.Sprintf("group %d sd=%d rm=%d ne=%v", g, sd, rm, ne)}

		const pool = 12
		for id := int64(1); id <= pool; id++ {
			if rng.Intn(2) == 0 {
				r := randRow(rng, id)
				if err := eng.SrcPut(r); err != nil {
					t.Fatal(err)
				}
				md.src[id] = naiveRow{d: clonePtr(r.D), c: cloneBytes(r.C)}
				log = append(log, "SrcPut "+fmtRow(r))
			}
			switch rng.Intn(3) {
			case 0, 1:
				r := randRow(rng, id)
				if err := eng.TgtPut(r); err != nil {
					t.Fatal(err)
				}
				md.tgt[id] = naiveRow{d: clonePtr(r.D), c: cloneBytes(r.C)}
				md.ver[id]++
				log = append(log, "TgtPut "+fmtRow(r))
			case 2:
				if rng.Intn(3) == 0 {
					if err := eng.TgtDel(id); err != nil {
						t.Fatal(err)
					}
					delete(md.tgt, id)
					md.ver[id]++
					log = append(log, fmt.Sprintf("TgtDel %d", id))
				}
			}
		}

		lo := int64(1 + rng.Intn(pool))
		hi := lo + int64(rng.Intn(pool-int(lo)+2))
		del := rng.Intn(2) == 1
		log = append(log, fmt.Sprintf("Compare/Plan [%d,%d) del=%v", lo, hi, del))

		got, err := eng.Compare(lo, hi)
		if err != nil {
			t.Fatalf("group %d Compare: %v%s", g, err, joinLog(log))
		}
		want := md.compare(lo, hi)
		assertResultsEqual(t, g, log, got, want)

		wantVisit := 0
		for _, wr := range want {
			if _, ok := md.src[wr.id]; ok {
				wantVisit++
			}
			if wr.ex {
				wantVisit++
			}
		}
		if vc := eng.LastVisitCount(); vc != wantVisit {
			t.Fatalf("group %d visit %d want %d%s", g, vc, wantVisit, joinLog(log))
		}

		p, err := Build(eng, lo, hi, del)
		if err != nil {
			t.Fatalf("group %d Build: %v%s", g, err, joinLog(log))
		}
		wi := md.plan(lo, hi, del)
		assertItemsEqual(t, g, log, p.Items, wi)

		// 计划生成后随机扰动一部分目标行，再对拍 Apply 的成/败。
		disturbed := false
		if rng.Intn(2) == 0 {
			disturbed = true
			id := int64(1 + rng.Intn(pool))
			switch rng.Intn(3) {
			case 0:
				r := randRow(rng, id)
				_ = eng.TgtPut(r)
				md.tgt[id] = naiveRow{d: clonePtr(r.D), c: cloneBytes(r.C)}
				md.ver[id]++
				log = append(log, "disturbance: TgtPut "+fmtRow(r))
			case 1:
				_ = eng.TgtDel(id)
				delete(md.tgt, id)
				md.ver[id]++
				log = append(log, fmt.Sprintf("disturbance: TgtDel %d", id))
			case 2:
				if _, ok := md.tgt[id]; !ok {
					r := randRow(rng, id)
					_ = eng.TgtPut(r)
					md.tgt[id] = naiveRow{d: clonePtr(r.D), c: cloneBytes(r.C)}
					md.ver[id]++
					log = append(log, "disturbance: TgtPut(insert) "+fmtRow(r))
				}
			}
		}

		hasDelete := false
		for _, it := range wi {
			if it.kind == KindDelete {
				hasDelete = true
			}
		}
		role := RoleRepairer
		if hasDelete || rng.Intn(2) == 0 {
			role = RoleAdmin
		}
		log = append(log, fmt.Sprintf("Apply role=%d items=%d", role, len(wi)))

		gi, gu, gd, gerr := Apply(eng, role, p)
		wiN, wu, wd, staleAt, wok := md.napply(wi)
		permDenied := hasDelete && role != RoleAdmin
		switch {
		case permDenied:
			if gerr == nil {
				t.Fatalf("group %d want permission denied%s\n%s", g, joinLog(log), dumpItems(wi))
			}
			log = append(log, "output: both permission-denied; verdict: match")
		case !wok:
			if !staleID(gerr, staleAt) {
				t.Fatalf("group %d want stale(%d) got %v%s\n%s", g, staleAt, gerr, joinLog(log), dumpItems(wi))
			}
			log = append(log, fmt.Sprintf("output: both ErrStale(%d); verdict: match", staleAt))
		default:
			if gerr != nil || gi != wiN || gu != wu || gd != wd {
				t.Fatalf("group %d apply counts (%d,%d,%d) err=%v want (%d,%d,%d)%s",
					g, gi, gu, gd, gerr, wiN, wu, wd, joinLog(log))
			}
			log = append(log, fmt.Sprintf("output: both applied ins=%d upd=%d del=%d; verdict: match", wiN, wu, wd))
			// 收敛性：无扰动时，Apply(Plan) 后 [lo,hi) 不应再有 Missing/Changed，
			// del=true 时也不应有 Extra。若发生过计划外扰动，则在扰动停止后重新
			// Build+Apply 一轮，再验证同一不变量（规格保证的是“无并发写”前提）。
			convergeDel := del
			if disturbed {
				p2, _ := Build(eng, lo, hi, true)
				if _, _, _, perr := Apply(eng, RoleAdmin, p2); perr != nil {
					t.Fatalf("group %d quiescent repair failed: %v%s", g, perr, joinLog(log))
				}
				convergeDel = true
			}
			after, _ := eng.Compare(lo, hi)
			for _, r := range after {
				if r.Kind == diff.Missing || r.Kind == diff.Changed {
					t.Fatalf("group %d post-apply convergence broken at id %d (%s)%s",
						g, r.ID, kind(r.Kind), joinLog(log))
				}
				if convergeDel && r.Kind == diff.Extra {
					t.Fatalf("group %d post-apply Extra remains at id %d (del=true)%s",
						g, r.ID, joinLog(log))
				}
			}
			log = append(log, "output: post-apply Compare converged; verdict: match")
		}
		t.Logf("RANDOM %s", joinLog(log))
	}
}
