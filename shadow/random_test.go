package shadow

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/delta"
	"ontology/doc"
)

// naiveModel recomputes everything from scratch after every accepted update:
// full-doc merge, full-leaf mv table, full delta. It is the reference oracle.
type naiveModel struct {
	desired  *doc.Node
	reported *doc.Node
	mvDes    map[string]int64
	mvRep    map[string]int64
	ver      int64
	delta    map[string]delta.Entry
	limit    int
}

func newNaive(limit int) *naiveModel {
	return &naiveModel{
		desired: doc.NewObject(), reported: doc.NewObject(),
		mvDes: map[string]int64{}, mvRep: map[string]int64{},
		delta: map[string]delta.Entry{}, limit: limit,
	}
}

// fullMerge merges patch into target with complete mv bookkeeping.
func fullMerge(target *doc.Node, mv map[string]int64, p doc.Patch, newVer, limit int64) (bool, error) {
	n := doc.Leaves(target)
	hooks := doc.Hooks{
		Set: func(path string, _, leaf *doc.Node) { mv[path] = newVer },
		Delete: func(path string, old *doc.Node) {
			if old.Kind != doc.Object {
				delete(mv, path)
				return
			}
			for _, f := range doc.Flatten(old) {
				delete(mv, path+"."+f.Path)
			}
		},
	}
	r, err := doc.Merge(target, p, &n, int(limit), hooks)
	return r.Changed, err
}

func fullDelta(desired, reported *doc.Node, mv map[string]int64) map[string]delta.Entry {
	out := map[string]delta.Entry{}
	for _, f := range doc.Flatten(desired) {
		rn := doc.Lookup(reported, f.Path)
		if !doc.EqualLeaves(f.Node, rn) {
			out[f.Path] = delta.Entry{Path: f.Path, Val: f.Node, MV: mv[f.Path]}
		}
	}
	return out
}

var keyPool = []string{"a", "b", "c", "d", "m", "x", "y", "k", "z", "e", "aa", "ab", "a.b"}

func randKey(r *rand.Rand) string {
	if r.Intn(5) == 0 {
		n := 1 + r.Intn(3)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('p' + r.Intn(4))
		}
		return string(b)
	}
	return keyPool[r.Intn(len(keyPool))]
}

func genValue(r *rand.Rand, path string, depth int) (*doc.Node, string) {
	roll := r.Intn(100)
	switch {
	case depth >= 4 || roll < 55:
		switch r.Intn(3) {
		case 0:
			v := r.Intn(5) - 1
			return doc.IntLeaf(int64(v)), fmt.Sprintf("%s=%d", path, v)
		case 1:
			v := []string{"x", "y", "1"}[r.Intn(3)]
			return doc.StringLeaf(v), fmt.Sprintf("%s=%q", path, v)
		default:
			v := r.Intn(2) == 0
			return doc.BoolLeaf(v), fmt.Sprintf("%s=%t", path, v)
		}
	case roll < 70:
		return doc.NullNode(), path + "=null"
	default:
		n := doc.NewObject()
		var parts []string
		for i := 0; i < 1+r.Intn(2); i++ {
			k := randKey(r)
			ch, desc := genValue(r, k, depth+1)
			n.Kids[k] = ch
			parts = append(parts, desc)
		}
		sort.Strings(parts)
		return n, path + ":{" + strings.Join(parts, ",") + "}"
	}
}

func genPatch(r *rand.Rand) (*doc.Node, string) {
	root := doc.NewObject()
	var parts []string
	for i := 0; i < 1+r.Intn(3); i++ {
		k := randKey(r)
		ch, desc := genValue(r, k, 1)
		root.Kids[k] = ch
		parts = append(parts, desc)
	}
	sort.Strings(parts)
	return root, "{" + strings.Join(parts, ",") + "}"
}

// TestRandomAgainstNaive replays random sequences and compares every
// observable outcome against a full-recomputation reference model.
func TestRandomAgainstNaive(t *testing.T) {
	if !testing.Verbose() {
		t.Log("rerun with -v for full input/output/reason logs")
	}
	const sequences = 1500
	rng := rand.New(rand.NewSource(20261004))

	for seq := 0; seq < sequences; seq++ {
		limit := 1 + rng.Intn(6)
		svc, _ := NewService(limit)
		dev := "dev"
		if err := svc.Create(dev); err != nil {
			t.Fatal(err)
		}
		m := newNaive(limit)

		nops := 1 + rng.Intn(12)
		logs := []string{fmt.Sprintf("seq=%d limit=%d ops=%d", seq, limit, nops)}

		for step := 0; step < nops; step++ {
			r := rng
			side := r.Intn(2)
			patch, pdesc := genPatch(r)
			var expect int64
			switch r.Intn(3) {
			case 0:
				expect = m.ver
			case 1:
				expect = m.ver + 1 + int64(r.Intn(3))
			}
			if r.Intn(12) == 0 {
				if r.Intn(2) == 0 {
					patch = doc.Obj("a.b", doc.IntLeaf(1))
				} else {
					patch = doc.Obj("a", doc.Obj("b", doc.Obj("c", doc.Obj("d", doc.Obj("e", doc.IntLeaf(1))))))
				}
				pdesc = "<invalid>"
			}

			sideName := "D"
			if side == 1 {
				sideName = "R"
			}
			input := fmt.Sprintf("step%d %s expect=%d patch=%s", step, sideName, expect, pdesc)

			// Reference: work on trial copies, commit only on success.
			refDes, refRep := doc.Clone(m.desired), doc.Clone(m.reported)
			refMvD, refMvR := copyMV(m.mvDes), copyMV(m.mvRep)
			refVer := m.ver
			deltaBefore := copyDelta(m.delta)

			var refErr error
			var refChanged bool
			p, perr := doc.NewPatch(patch)
			switch {
			case perr != nil:
				refErr = ErrInvalid
			case expect != 0 && expect != m.ver:
				refErr = ErrVersion
			default:
				target, mv := refDes, refMvD
				if side == 1 {
					target, mv = refRep, refMvR
				}
				ch, merr := fullMerge(target, mv, p, m.ver+1, int64(limit))
				if merr != nil {
					refErr = ErrTooLarge
				} else {
					refChanged = ch
					if ch {
						refVer = m.ver + 1
					}
				}
			}
			var refDeltaAfter map[string]delta.Entry
			if refErr == nil {
				refDeltaAfter = fullDelta(refDes, refRep, refMvD)
			}

			var got Result
			var gotErr error
			if side == 0 {
				got, gotErr = svc.UpdateDesired(dev, patch, expect)
			} else {
				got, gotErr = svc.UpdateReported(dev, patch, expect)
			}

			reason := ""
			switch {
			case refErr != nil:
				reason = "rejected:" + errName(refErr)
				if errName(gotErr) != errName(refErr) {
					t.Fatalf("%s\n%s\nref err=%v got err=%v", strings.Join(logs, "\n"), input, refErr, gotErr)
				}
			case refChanged:
				m.desired, m.reported = refDes, refRep
				m.mvDes, m.mvRep = refMvD, refMvR
				m.ver = refVer
				m.delta = refDeltaAfter
				reason = fmt.Sprintf("accepted ver=%d up=%v rm=%v", m.ver, renderEntries(got.Upsert), got.Remove)
				if gotErr != nil || got.NoChange || got.Version != m.ver {
					t.Fatalf("%s\n%s\nref accepted ver=%d got=%+v err=%v", strings.Join(logs, "\n"), input, m.ver, got, gotErr)
				}
				verifyMovement(t, deltaBefore, refDeltaAfter, got)
			default:
				reason = fmt.Sprintf("nochange ver=%d", m.ver)
				if gotErr != nil || !got.NoChange || got.Version != m.ver {
					t.Fatalf("%s\n%s\nref nochange ver=%d got=%+v err=%v", strings.Join(logs, "\n"), input, m.ver, got, gotErr)
				}
				if len(got.Upsert) != 0 || len(got.Remove) != 0 {
					t.Fatalf("%s\n%s\nnochange produced delta movement", strings.Join(logs, "\n"), input)
				}
			}
			logs = append(logs, "  "+input+" -> "+reason)

			gd, gr, gmvD, gmvR, gver, _ := svc.Snapshot(dev)
			if gver != m.ver {
				t.Fatalf("ver mismatch ref=%d got=%d", m.ver, gver)
			}
			if !sameFlat(flatMap(m.desired), flatMap(gd)) {
				t.Fatalf("desired mismatch\nref=%v\ngot=%v", flatMap(m.desired), flatMap(gd))
			}
			if !sameFlat(flatMap(m.reported), flatMap(gr)) {
				t.Fatalf("reported mismatch\nref=%v\ngot=%v", flatMap(m.reported), flatMap(gr))
			}
			if !sameMV(m.mvDes, gmvD) || !sameMV(m.mvRep, gmvR) {
				t.Fatalf("mv mismatch refDes=%v gotDes=%v refRep=%v gotRep=%v", m.mvDes, gmvD, m.mvRep, gmvR)
			}
			gdelta, _ := svc.GetDelta(dev)
			if !sameDelta(m.delta, gdelta) {
				t.Fatalf("delta mismatch\nref=%v\ngot=%v", m.delta, gdelta)
			}
		}
		if testing.Verbose() {
			t.Log(strings.Join(logs, "\n"))
		}
	}
}

// verifyMovement checks the incremental Upsert/Remove exactly describe the
// transition from before to after, and are correctly ordered.
func verifyMovement(t *testing.T, before, after map[string]delta.Entry, got Result) {
	// Remove: in before, not in after.
	var wantRemove []string
	for p := range before {
		if _, ok := after[p]; !ok {
			wantRemove = append(wantRemove, p)
		}
	}
	// Upsert: new, or present but val/mv changed.
	var wantUpsert []delta.Entry
	for p, e := range after {
		b, ok := before[p]
		if !ok || b.MV != e.MV || !doc.EqualLeaves(b.Val, e.Val) {
			wantUpsert = append(wantUpsert, e)
		}
	}
	sort.Slice(wantUpsert, func(i, j int) bool { return wantUpsert[i].Path < wantUpsert[j].Path })
	sort.Strings(wantRemove)
	if len(wantRemove) != len(got.Remove) {
		t.Fatalf("remove=%v want %v", got.Remove, wantRemove)
	}
	for i := range wantRemove {
		if wantRemove[i] != got.Remove[i] {
			t.Fatalf("remove=%v want %v", got.Remove, wantRemove)
		}
	}
	if len(wantUpsert) != len(got.Upsert) {
		t.Fatalf("upsert=%v want %v", renderEntries(got.Upsert), renderEntries(wantUpsert))
	}
	for i := range wantUpsert {
		a, b := wantUpsert[i], got.Upsert[i]
		if a.Path != b.Path || a.MV != b.MV || !doc.EqualLeaves(a.Val, b.Val) {
			t.Fatalf("upsert=%v want %v", renderEntries(got.Upsert), renderEntries(wantUpsert))
		}
	}
}

func copyMV(m map[string]int64) map[string]int64 {
	c := make(map[string]int64, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyDelta(m map[string]delta.Entry) map[string]delta.Entry {
	c := make(map[string]delta.Entry, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func renderEntries(es []delta.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		switch e.Val.Kind {
		case doc.Int:
			out[i] = fmt.Sprintf("%s=i%d(mv%d)", e.Path, e.Val.I, e.MV)
		case doc.Str:
			out[i] = fmt.Sprintf("%s=%q(mv%d)", e.Path, e.Val.S, e.MV)
		default:
			out[i] = fmt.Sprintf("%s=%t(mv%d)", e.Path, e.Val.B, e.MV)
		}
	}
	return out
}

func flatMap(root *doc.Node) map[string]*doc.Node {
	out := map[string]*doc.Node{}
	for _, f := range doc.Flatten(root) {
		out[f.Path] = f.Node
	}
	return out
}

func sameFlat(ref, got map[string]*doc.Node) bool {
	if len(ref) != len(got) {
		return false
	}
	for p, a := range ref {
		b, ok := got[p]
		if !ok || !doc.EqualLeaves(a, b) {
			return false
		}
	}
	return true
}

func sameMV(ref, got map[string]int64) bool {
	if len(ref) != len(got) {
		return false
	}
	for k, v := range ref {
		if got[k] != v {
			return false
		}
	}
	return true
}

func sameDelta(ref map[string]delta.Entry, got []delta.Entry) bool {
	if len(ref) != len(got) {
		return false
	}
	for _, e := range got {
		r, ok := ref[e.Path]
		if !ok || r.MV != e.MV || !doc.EqualLeaves(r.Val, e.Val) {
			return false
		}
	}
	return true
}
