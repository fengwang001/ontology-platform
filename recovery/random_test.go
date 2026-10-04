package recovery

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/history"
	"ontology/lease"
)

type naiveLease struct{ r, last int64 }

type naiveOp struct {
	seq int64
	id  string
	del bool
}

type naive struct {
	E      int64
	lmax   int
	now    int64
	maxSeq int64
	gcp    int64
	h      int64
	ops    []naiveOp
	latest map[string]int64
	alive  map[string]int64
	ls     map[string]naiveLease
}

func newNaive(E int64, lmax int) *naive {
	return &naive{
		E:      E,
		lmax:   lmax,
		h:      1,
		latest: map[string]int64{},
		alive:  map[string]int64{},
		ls:     map[string]naiveLease{},
	}
}

func (n *naive) validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }
func (n *naive) validID(id string) bool  { return len(id) >= 1 && len(id) <= 256 }

type actOut struct {
	seq     int64
	cleared int
	removed []string
	floor   int64
}

// do 严格照抄题面规则，返回错误种类与 merge 细节。
func (n *naive) do(kd string, now int64, name, id string, r, g int64) (string, actOut) {
	switch kd {
	case "index":
		if !n.validNow(now) || !n.validID(id) {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		n.now, n.maxSeq = now, n.maxSeq+1
		n.ops = append(n.ops, naiveOp{n.maxSeq, id, false})
		n.latest[id] = n.maxSeq
		n.alive[id] = n.maxSeq
		return "ok", actOut{seq: n.maxSeq}
	case "delete":
		if !n.validNow(now) || !n.validID(id) {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		if _, ok := n.alive[id]; !ok {
			return "notfound", actOut{}
		}
		n.now, n.maxSeq = now, n.maxSeq+1
		n.ops = append(n.ops, naiveOp{n.maxSeq, id, true})
		n.latest[id] = n.maxSeq
		delete(n.alive, id)
		return "ok", actOut{seq: n.maxSeq}
	case "gcp":
		if !n.validNow(now) || g < 0 || g > n.maxSeq {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		if g < n.gcp {
			return "gcpback", actOut{}
		}
		n.now, n.gcp = now, g
		return "ok", actOut{}
	case "add":
		if !n.validNow(now) || name == "" || r < 1 || r > n.maxSeq+1 {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		if _, ok := n.ls[name]; ok {
			return "leaseexists", actOut{}
		}
		if r < n.h {
			return "unavail", actOut{}
		}
		if len(n.ls) >= n.lmax {
			return "limit", actOut{}
		}
		n.now = now
		n.ls[name] = naiveLease{r, now}
		return "ok", actOut{}
	case "renew":
		if !n.validNow(now) || name == "" || r < 1 || r > n.maxSeq+1 {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		l, ok := n.ls[name]
		if !ok {
			return "leasenotfound", actOut{}
		}
		if r < l.r {
			return "leaseback", actOut{}
		}
		n.now = now
		n.ls[name] = naiveLease{r, now}
		return "ok", actOut{}
	case "remove":
		if !n.validNow(now) || name == "" {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		if _, ok := n.ls[name]; !ok {
			return "leasenotfound", actOut{}
		}
		n.now = now
		delete(n.ls, name)
		return "ok", actOut{}
	case "merge":
		if !n.validNow(now) {
			return "invalid", actOut{}
		}
		if now < n.now {
			return "clockback", actOut{}
		}
		n.now = now
		var removed []string
		for nm, l := range n.ls {
			if now-l.last > n.E {
				removed = append(removed, nm)
				delete(n.ls, nm)
			}
		}
		sort.Strings(removed)
		floor := n.gcp + 1
		for _, l := range n.ls {
			if l.r < floor {
				floor = l.r
			}
		}
		cleared := 0
		for _, op := range n.ops {
			if op.seq >= n.h && op.seq < floor {
				if op.del || n.latest[op.id] > op.seq {
					cleared++
				}
			}
		}
		n.h = floor
		return "ok", actOut{cleared: cleared, removed: removed, floor: floor}
	default:
		panic("bad kind")
	}
}

func (n *naive) plan(c int64) (string, []int64, map[string]int64) {
	if c+1 >= n.h {
		var seqs []int64
		for _, op := range n.ops {
			if op.seq > c {
				seqs = append(seqs, op.seq)
			}
		}
		return "ops", seqs, nil
	}
	docs := map[string]int64{}
	for id, seq := range n.alive {
		docs[id] = seq
	}
	return "file", nil, docs
}

type action struct {
	kind     string
	now      int64
	name, id string
	r, g     int64
}

func (a action) String() string {
	return fmt.Sprintf("%s(now=%d name=%q id=%q r=%d g=%d)",
		a.kind, a.now, a.name, a.id, a.r, a.g)
}

func TestRandomVsNaive(t *testing.T) {
	const groups = 1500
	rng := rand.New(rand.NewSource(20261004))
	ids := []string{"a", "b", "c", "d"}
	names := []string{"L1", "L2", "L3"}

	for gi := 0; gi < groups; gi++ {
		E := int64(1 + rng.Intn(5))
		lmax := 1 + rng.Intn(3)
		h := history.NewHistory()
		s, err := lease.NewSet(h, E, lmax)
		if err != nil {
			t.Fatal(err)
		}
		pl := NewPlanner(h)
		nm := newNaive(E, lmax)

		var log bytes.Buffer
		fmt.Fprintf(&log, "[group %d E=%d lmax=%d]\n", gi, E, lmax)

		checkPlans := func(stage string) {
			t.Helper()
			for c := int64(0); c <= nm.maxSeq; c++ {
				wantMode, wantSeqs, wantDocs := nm.plan(c)
				got, perr := pl.Plan(c)
				if perr != nil {
					t.Fatalf("g%d %s Plan(%d) err=%v\n%s", gi, stage, c, perr, log.String())
				}
				mode := "ops"
				if got.Mode == FileBased {
					mode = "file"
				}
				if mode != wantMode {
					t.Fatalf("g%d %s Plan(%d) mode=%s want %s\n%s", gi, stage, c, mode, wantMode, log.String())
				}
				if mode == "ops" {
					if len(got.Ops) != len(wantSeqs) {
						t.Fatalf("g%d %s Plan(%d) opslen=%d want %d\n%s", gi, stage, c, len(got.Ops), len(wantSeqs), log.String())
					}
					// 副本在 c 的状态：对 seq<=c 的现存操作重放（c+1>=H 保证 1..c 无洞）。
					rep := map[string]bool{}
					for _, op := range nm.ops {
						if op.seq > c {
							break
						}
						if op.del {
							delete(rep, op.id)
						} else {
							rep[op.id] = true
						}
					}
					for i, op := range got.Ops {
						if op.Seq != wantSeqs[i] {
							t.Fatalf("g%d %s Plan(%d) ops[%d]=%d want %d\n%s", gi, stage, c, i, op.Seq, wantSeqs[i], log.String())
						}
						if i > 0 && op.Seq != got.Ops[i-1].Seq+1 {
							t.Fatalf("g%d %s Plan(%d) ops non-contiguous\n%s", gi, stage, c, log.String())
						}
						if op.Kind == history.KindIndex {
							rep[string(op.ID)] = true
						} else {
							delete(rep, string(op.ID))
						}
					}
					for id := range nm.alive {
						if !rep[id] {
							t.Fatalf("g%d %s Plan(%d) replay missing live %s\n%s", gi, stage, c, id, log.String())
						}
					}
					for id := range rep {
						if _, ok := nm.alive[id]; !ok {
							t.Fatalf("g%d %s Plan(%d) replay stale %s\n%s", gi, stage, c, id, log.String())
						}
					}
				} else {
					if len(got.Docs) != len(wantDocs) {
						t.Fatalf("g%d %s Plan(%d) docslen=%d want %d\n%s", gi, stage, c, len(got.Docs), len(wantDocs), log.String())
					}
					var prev []byte
					for _, d := range got.Docs {
						if prev != nil && bytes.Compare(prev, d.ID) >= 0 {
							t.Fatalf("g%d %s Plan(%d) docs not byte-sorted\n%s", gi, stage, c, log.String())
						}
						prev = d.ID
						if wantDocs[string(d.ID)] != d.Seq {
							t.Fatalf("g%d %s Plan(%d) doc %s seq=%d want %d\n%s", gi, stage, c, d.ID, d.Seq, wantDocs[string(d.ID)], log.String())
						}
					}
					if got.MaxSeq != nm.maxSeq {
						t.Fatalf("g%d %s Plan(%d) maxSeq=%d want %d\n%s", gi, stage, c, got.MaxSeq, nm.maxSeq, log.String())
					}
				}
			}
		}

		steps := 20 + rng.Intn(40)
		for si := 0; si < steps; si++ {
			a := action{now: nm.now}
			switch rng.Intn(9) {
			case 0:
				a.kind, a.id = "index", ids[rng.Intn(len(ids))]
			case 1:
				a.kind, a.id = "delete", ids[rng.Intn(len(ids))]
			case 2:
				a.kind = "gcp"
				if nm.maxSeq > 0 {
					a.g = rng.Int63n(nm.maxSeq + 1)
				}
			case 3:
				a.kind, a.name = "add", names[rng.Intn(len(names))]
				if nm.maxSeq > 0 && rng.Intn(3) == 0 {
					a.r = 1
				} else {
					a.r = 1 + rng.Int63n(nm.maxSeq+1)
				}
			case 4:
				a.kind, a.name = "renew", names[rng.Intn(len(names))]
				a.r = 1 + rng.Int63n(nm.maxSeq+1)
			case 5:
				a.kind, a.name = "remove", names[rng.Intn(len(names))]
			case 6:
				a.kind = "merge"
			case 7:
				a.now = nm.now + E + int64(rng.Intn(2))
				a.kind = []string{"merge", "renew", "add"}[rng.Intn(3)]
				if a.kind != "merge" {
					a.name = names[rng.Intn(len(names))]
					a.r = 1 + rng.Int63n(nm.maxSeq+1)
				}
			case 8:
				a.kind = []string{"index", "delete", "add", "gcp"}[rng.Intn(4)]
				a.id = ids[rng.Intn(len(ids))]
				a.name = names[rng.Intn(len(names))]
				if rng.Intn(2) == 0 && nm.now > 0 {
					a.now = nm.now - 1
				} else {
					a.now = 1_000_000_000_001
				}
				a.r = 1 + rng.Int63n(nm.maxSeq+1)
			}

			wantKind, wantOut := nm.do(a.kind, a.now, a.name, a.id, a.r, a.g)

			var gotErr error
			var gotSeq int64
			var gotMR history.MergeResult
			switch a.kind {
			case "index":
				gotSeq, gotErr = h.Index(a.now, []byte(a.id))
			case "delete":
				gotSeq, gotErr = h.Delete(a.now, []byte(a.id))
			case "gcp":
				gotErr = s.SetGlobalCheckpoint(a.now, a.g)
			case "add":
				gotErr = s.AddLease(a.now, a.name, a.r)
			case "renew":
				gotErr = s.RenewLease(a.now, a.name, a.r)
			case "remove":
				gotErr = s.RemoveLease(a.now, a.name)
			case "merge":
				gotMR, gotErr = s.Merge(a.now)
			}
			gotKind := errKind(gotErr)
			basis := ""
			if a.kind == "merge" && wantKind == "ok" {
				basis = fmt.Sprintf(" [floor=%d cleared=%d removed=%v]", wantOut.floor, wantOut.cleared, wantOut.removed)
			}
			fmt.Fprintf(&log, "  %s => want=%s got=%s%s\n", a, wantKind, gotKind, basis)
			if gotKind != wantKind {
				t.Fatalf("g%d step %d %s: want %s got %s\n%s", gi, si, a, wantKind, gotKind, log.String())
			}
			if wantKind == "ok" {
				switch a.kind {
				case "index", "delete":
					if gotSeq != wantOut.seq {
						t.Fatalf("g%d %s seq=%d want %d\n%s", gi, a.kind, gotSeq, wantOut.seq, log.String())
					}
				case "merge":
					if gotMR.Cleared != wantOut.cleared {
						t.Fatalf("g%d merge cleared=%d want %d\n%s", gi, gotMR.Cleared, wantOut.cleared, log.String())
					}
					if len(gotMR.RemovedLeases) != len(wantOut.removed) {
						t.Fatalf("g%d merge removed=%v want %v\n%s", gi, gotMR.RemovedLeases, wantOut.removed, log.String())
					}
					for i := range wantOut.removed {
						if gotMR.RemovedLeases[i] != wantOut.removed[i] {
							t.Fatalf("g%d merge removed[%d]=%s want %s\n%s", gi, i, gotMR.RemovedLeases[i], wantOut.removed[i], log.String())
						}
					}
				}
			}

			if a.kind == "merge" && wantKind == "ok" {
				checkPlans(fmt.Sprintf("after step %d", si))
			}
		}

		// 序列结束后再整体校验一次所有 c。
		checkPlans("final")
		if gi < 3 || gi == groups-1 {
			t.Logf("\n%s", log.String())
		}
	}
}
