package clearance

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/grant"
	"ontology/territory"
)

// 朴素模型：按叶集合逐叶枚举，与 grant/clearance 的实现完全独立。

type naiveRec struct {
	id, title, licensee string
	leaves              map[string]bool
	start, end          int64
	exclusive           bool
}

type naiveWorld struct {
	tr     *territory.Tree
	leaves []string
	clock  int64
	grants map[string]*naiveRec
}

func newNaiveWorld(tr *territory.Tree, leaves []string) *naiveWorld {
	return &naiveWorld{tr: tr, leaves: leaves, grants: map[string]*naiveRec{}}
}

func (n *naiveWorld) inside(leaf, anc string) bool {
	return leaf == anc || n.tr.IsProperDescendant(anc, leaf)
}

func (n *naiveWorld) leafSet(node string, excludes []string) map[string]bool {
	set := map[string]bool{}
	for _, lf := range n.leaves {
		if !n.inside(lf, node) {
			continue
		}
		hit := false
		for _, e := range excludes {
			if n.inside(lf, e) {
				hit = true
				break
			}
		}
		if !hit {
			set[lf] = true
		}
	}
	return set
}

func strDup(xs []string) bool {
	seen := map[string]bool{}
	for _, x := range xs {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}

func (n *naiveWorld) conflict(c *naiveRec, skip string) (why, minID string) {
	ids := make([]string, 0, len(n.grants))
	for id := range n.grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := n.grants[id]
		if id == skip || g.title != c.title || g.licensee == c.licensee {
			continue
		}
		if !c.exclusive && !g.exclusive {
			continue
		}
		if c.end <= g.start || g.end <= c.start {
			continue
		}
		shared := ""
		for _, lf := range n.leaves {
			if c.leaves[lf] && g.leaves[lf] {
				shared = lf
				break
			}
		}
		if shared == "" {
			continue
		}
		return fmt.Sprintf("shared leaf=%s window=[%d,%d) vs id=%s [%d,%d) excl=%v/%v",
			shared, c.start, c.end, id, g.start, g.end, c.exclusive, g.exclusive), id
	}
	return "", ""
}

// 返回 (类别, 依据)；类别 "" 表示接受。
func (n *naiveWorld) add(now int64, id, title, lic, node string, excl []string, s, e int64, ex bool) (string, string) {
	if now < 0 || now > 1e12 || id == "" || title == "" || lic == "" || node == "" ||
		s < 0 || s > 1e12 || e < 0 || e > 1e12 || s >= e || len(excl) > 8 || strDup(excl) {
		return "invalid", "field out of range / start>=end / bad excludes"
	}
	if now < n.clock {
		return "clock", fmt.Sprintf("now=%d<clock=%d", now, n.clock)
	}
	if _, ok := n.grants[id]; ok {
		return "dup", "id exists"
	}
	if !n.tr.Has(node) {
		return "unknown", "node unknown"
	}
	for _, x := range excl {
		if !n.tr.Has(x) {
			return "unknown", "exclude unknown"
		}
	}
	for _, x := range excl {
		if !n.tr.IsProperDescendant(node, x) {
			return "badexcl", "exclude not proper descendant"
		}
	}
	for i := range excl {
		for j := i + 1; j < len(excl); j++ {
			if n.tr.IsProperDescendant(excl[i], excl[j]) ||
				n.tr.IsProperDescendant(excl[j], excl[i]) {
				return "badexcl", "excludes ancestor-related"
			}
		}
	}
	set := n.leafSet(node, excl)
	if len(set) == 0 {
		return "empty", "all leaves excluded"
	}
	cand := &naiveRec{id, title, lic, set, s, e, ex}
	if why, who := n.conflict(cand, ""); who != "" {
		return "conflict:" + who, why
	}
	n.grants[id] = cand
	n.clock = now
	return "", "accepted"
}

func (n *naiveWorld) revoke(now int64, id string) (string, string) {
	if now < 0 || now > 1e12 || id == "" {
		return "invalid", "bad arg"
	}
	if now < n.clock {
		return "clock", "clock back"
	}
	g, ok := n.grants[id]
	if !ok {
		return "notfound", "missing"
	}
	if now >= g.end {
		return "expired", fmt.Sprintf("now=%d>=end=%d", now, g.end)
	}
	if now <= g.start {
		delete(n.grants, id)
	} else {
		g.end = now
	}
	n.clock = now
	return "", "accepted"
}

func (n *naiveWorld) extend(now int64, id string, newEnd int64) (string, string) {
	if now < 0 || now > 1e12 || id == "" || newEnd < 0 || newEnd > 1e12 {
		return "invalid", "bad arg"
	}
	if now < n.clock {
		return "clock", "clock back"
	}
	g, ok := n.grants[id]
	if !ok {
		return "notfound", "missing"
	}
	if g.end <= now {
		return "expired", "end<=now"
	}
	if !(newEnd > g.end) {
		return "notextended", "newEnd<=end"
	}
	cand := *g
	cand.end = newEnd
	if why, who := n.conflict(&cand, id); who != "" {
		return "conflict:" + who, why
	}
	g.end = newEnd
	n.clock = now
	return "", "accepted"
}

func (n *naiveWorld) active(title, leaf string, t int64) []*naiveRec {
	var out []*naiveRec
	for _, g := range n.grants {
		if g.title == title && g.leaves[leaf] && g.start <= t && t < g.end {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (n *naiveWorld) canPlay(title, lic, leaf string, t int64) (string, string) {
	own, blocked := false, ""
	for _, g := range n.active(title, leaf, t) {
		if g.licensee == lic {
			own = true
		} else if g.exclusive && (blocked == "" || g.id < blocked) {
			blocked = g.id
		}
	}
	switch {
	case own:
		return VerdictAllow, "own cover"
	case blocked != "":
		return VerdictExclusive, "blocked by " + blocked
	default:
		return VerdictNoLicense, "no cover"
	}
}

type rndOp struct {
	kind                 byte
	now                  int64
	id, title, lic, node string
	excl                 []string
	s, e, newEnd         int64
	exclusive            bool
	qTitle, qLic, qLeaf  string
	qT                   int64
}

func randomTree(rng *rand.Rand) (*territory.Tree, []string, []string) {
	kids := map[string][]string{"WORLD": {}}
	var all, leaves []string
	nReg := 2 + rng.Intn(2)
	for r := 0; r < nReg; r++ {
		rn := fmt.Sprintf("r%d", r)
		kids["WORLD"] = append(kids["WORLD"], rn)
		all = append(all, rn)
		nSub := 1 + rng.Intn(2)
		for s := 0; s < nSub; s++ {
			sn := fmt.Sprintf("%ss%d", rn, s)
			kids[rn] = append(kids[rn], sn)
			all = append(all, sn)
			nLeaf := 1 + rng.Intn(2)
			for l := 0; l < nLeaf; l++ {
				ln := fmt.Sprintf("%sl%d", sn, l)
				kids[sn] = append(kids[sn], ln)
				all = append(all, ln)
				leaves = append(leaves, ln)
			}
		}
	}
	all = append([]string{"WORLD"}, all...)
	tr, err := territory.New(kids)
	if err != nil {
		panic(err)
	}
	return tr, leaves, all
}

func genOps(rng *rand.Rand, all, leaves []string) []rndOp {
	titles := []string{"T1", "T2", "T3"}
	lics := []string{"甲", "乙", "丙"}
	ids := []string{"g0", "g1", "g2", "g3", "g4"}
	pick := func(p []string) string { return p[rng.Intn(len(p))] }
	ops := make([]rndOp, 14)
	var clock int64
	for i := range ops {
		o := rndOp{now: clock + int64(rng.Intn(3))}
		if rng.Intn(12) == 0 {
			if clock > 0 {
				o.now = clock - 1 - int64(rng.Intn(int(clock)))
			}
		}
		switch rng.Intn(10) {
		case 0, 1:
			o.kind = 'R'
			o.id = pick(ids)
			if rng.Intn(8) == 0 {
				o.id = "ghost"
			}
		case 2, 3:
			o.kind = 'E'
			o.id = pick(ids)
			o.newEnd = int64(50 + rng.Intn(350))
		default:
			o.kind = 'A'
			o.id = pick(ids)
			o.title = pick(titles)
			o.lic = pick(lics)
			if rng.Intn(10) == 0 {
				o.node = "mars"
			} else {
				o.node = pick(all)
			}
			nEx := rng.Intn(4)
			if rng.Intn(20) == 0 {
				nEx = 9
			}
			for k := 0; k < nEx; k++ {
				if rng.Intn(10) == 0 {
					o.excl = append(o.excl, "mars")
				} else {
					o.excl = append(o.excl, pick(all))
				}
			}
			o.s = int64(rng.Intn(300))
			if rng.Intn(10) == 0 {
				o.e = int64(rng.Intn(300))
			} else {
				o.e = o.s + 1 + int64(rng.Intn(100))
			}
			o.exclusive = rng.Intn(2) == 0
		}
		o.qTitle = pick(titles)
		o.qLic = pick(lics)
		o.qLeaf = pick(leaves)
		o.qT = int64(rng.Intn(320))
		ops[i] = o
		clock = o.now
	}
	return ops
}

func classifyErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, grant.ErrInvalidArg):
		return "invalid"
	case errors.Is(err, grant.ErrClockBack):
		return "clock"
	case errors.Is(err, grant.ErrDuplicateID):
		return "dup"
	case errors.Is(err, grant.ErrUnknownRegion):
		return "unknown"
	case errors.Is(err, grant.ErrBadExcludes):
		return "badexcl"
	case errors.Is(err, grant.ErrEmptyCoverage):
		return "empty"
	case errors.Is(err, grant.ErrNotFound):
		return "notfound"
	case errors.Is(err, grant.ErrExpired):
		return "expired"
	case errors.Is(err, grant.ErrNotExtended):
		return "notextended"
	case errors.Is(err, grant.ErrConflict):
		var ce *grant.ConflictError
		if errors.As(err, &ce) {
			return "conflict:" + ce.ID
		}
		return "conflict"
	default:
		return "OTHER:" + err.Error()
	}
}

func runOneSequence(t *testing.T, seed int64, verbose bool) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	tr, leaves, all := randomTree(rng)
	ops := genOps(rng, all, leaves)

	run := func() []string {
		reg := grant.NewRegistry(tr)
		eng := New(reg)
		nw := newNaiveWorld(tr, leaves)
		trace := make([]string, 0, len(ops)*2)
		for i, o := range ops {
			var got, want, why string
			switch o.kind {
			case 'A':
				err := reg.Add(o.now, o.id, o.title, o.lic, o.node, o.excl, o.s, o.e, o.exclusive)
				got = classifyErr(err)
				want, why = nw.add(o.now, o.id, o.title, o.lic, o.node, o.excl, o.s, o.e, o.exclusive)
				line := fmt.Sprintf("[seed=%d #%d] Add(now=%d id=%s title=%s lic=%s node=%s excl=%v [%d,%d) ex=%v) -> %s | naive=%s (%s)",
					seed, i, o.now, o.id, o.title, o.lic, o.node, o.excl, o.s, o.e, o.exclusive, got, want, why)
				trace = append(trace, line)
			case 'R':
				err := reg.Revoke(o.now, o.id)
				got = classifyErr(err)
				want, why = nw.revoke(o.now, o.id)
				trace = append(trace, fmt.Sprintf("[seed=%d #%d] Revoke(now=%d id=%s) -> %s | naive=%s (%s)",
					seed, i, o.now, o.id, got, want, why))
			case 'E':
				err := reg.Extend(o.now, o.id, o.newEnd)
				got = classifyErr(err)
				want, why = nw.extend(o.now, o.id, o.newEnd)
				trace = append(trace, fmt.Sprintf("[seed=%d #%d] Extend(now=%d id=%s newEnd=%d) -> %s | naive=%s (%s)",
					seed, i, o.now, o.id, o.newEnd, got, want, why))
			}
			if got != want {
				t.Fatalf("seed=%d op#%d mismatch: got=%q naive=%q\n%s", seed, i, got, want, trace[len(trace)-1])
			}

			// 查询对照：CanPlay 与 Holders。
			d, err := eng.CanPlay(o.qTitle, o.qLic, o.qLeaf, o.qT)
			if err != nil {
				t.Fatalf("seed=%d CanPlay err: %v", seed, err)
			}
			nv, nwhy := nw.canPlay(o.qTitle, o.qLic, o.qLeaf, o.qT)
			if d.Verdict == VerdictExclusive {
				wantID := ""
				for _, g := range nw.active(o.qTitle, o.qLeaf, o.qT) {
					if g.licensee != o.qLic && g.exclusive && (wantID == "" || g.id < wantID) {
						wantID = g.id
					}
				}
				if d.ExclusiveID != wantID {
					t.Fatalf("seed=%d CanPlay exclusive id: got=%q naive=%q", seed, d.ExclusiveID, wantID)
				}
			}
			if d.Verdict != nv {
				t.Fatalf("seed=%d CanPlay verdict: got=%s naive=%s (%s)", seed, d.Verdict, nv, nwhy)
			}
			hs, err := eng.Holders(o.qTitle, o.qLeaf, o.qT)
			if err != nil {
				t.Fatalf("seed=%d Holders err: %v", seed, err)
			}
			na := nw.active(o.qTitle, o.qLeaf, o.qT)
			if len(hs) != len(na) {
				t.Fatalf("seed=%d Holders len: got=%d naive=%d", seed, len(hs), len(na))
			}
			for k := range hs {
				if hs[k].ID != na[k].id {
					t.Fatalf("seed=%d Holders order: %v vs %v", seed, hs, na)
				}
			}
			trace = append(trace, fmt.Sprintf("    query CanPlay(%s,%s,%s,t=%d)=%s/%s Holders=%d (%s)",
				o.qTitle, o.qLic, o.qLeaf, o.qT, d.Verdict, d.ExclusiveID, len(hs), nwhy))
		}
		return trace
	}

	first := run()
	// 确定性重放：相同序列再跑一遍，trace 必须完全一致。
	second := run()
	if len(first) != len(second) {
		t.Fatalf("seed=%d replay length differs", seed)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("seed=%d nondeterministic at %d:\n%s\n%s", seed, i, first[i], second[i])
		}
	}
	if verbose {
		for _, l := range first {
			t.Log(l)
		}
	}
	return first
}

func TestRandomVsNaive1500(t *testing.T) {
	const N = 1500
	var sample []string
	for seed := int64(1); seed <= N; seed++ {
		trace := runOneSequence(t, seed, seed <= 3)
		if seed <= 3 {
			sample = append(sample, trace...)
		}
	}
	t.Logf("compared %d random operation sequences against naive leaf-set simulation (3 fully logged, %d log lines)",
		N, len(sample))
}
