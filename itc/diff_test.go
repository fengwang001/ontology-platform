package itc

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// op is one recorded step in a random scenario.
type op struct {
	kind string
	a, b string
}

// runReplay executes ops against a fresh production registry and returns the
// captured outputs. It is used both for the differential test and for the
// deterministic replay test.
func runReplay(t *testing.T, ops []op) []string {
	t.Helper()
	r := NewRegistry()
	var out []string
	for _, o := range ops {
		switch o.kind {
		case "seed":
			err := r.Seed(o.a)
			if err == nil {
				out = append(out, mustString(t, r, o.a))
			} else {
				out = append(out, errCode(err))
			}
		case "fork":
			p, c, err := r.Fork(o.a, o.b)
			out = append(out, errOr2(err, p, c))
		case "event":
			err := r.Event(o.a)
			s := ""
			if err == nil {
				s = mustString(t, r, o.a)
			}
			out = append(out, errOr1(err, s))
		case "peek":
			s, err := r.Peek(o.a)
			out = append(out, errOr1(err, s))
		case "join":
			s, err := r.Join(o.a, o.b)
			out = append(out, errOr1(err, s))
		case "compare":
			rel, err := r.Compare(o.a, o.b)
			out = append(out, errOr1(err, rel.String()))
		case "string":
			s, err := r.String(o.a)
			out = append(out, errOr1(err, s))
		default:
			t.Fatalf("unknown op %q", o.kind)
		}
	}
	return out
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrEmptyName):
		return "empty"
	case errors.Is(err, ErrNameExists):
		return "exists"
	case errors.Is(err, ErrUnknownName):
		return "unknown"
	case errors.Is(err, ErrSameName):
		return "same"
	case errors.Is(err, ErrIdentityOverlap):
		return "overlap"
	default:
		return "other"
	}
}

func errOr1(err error, s string) string {
	if err != nil {
		return errCode(err)
	}
	return s
}

func errOr2(err error, a, b string) string {
	if err != nil {
		return errCode(err)
	}
	return a + "|" + b
}

func runNaive(ops []op) []string {
	r := newNreg()
	var out []string
	for _, o := range ops {
		var res nresult
		switch o.kind {
		case "seed":
			res = r.seed(o.a)
		case "fork":
			res = r.fork(o.a, o.b)
		case "event":
			res = r.event(o.a)
		case "peek":
			res = r.peek(o.a)
		case "join":
			res = r.join(o.a, o.b)
		case "compare":
			res = r.compare(o.a, o.b)
		case "string":
			res = r.str(o.a)
		}
		if res.err != "" {
			code := res.err
			if code == "unknown1" || code == "unknown2" {
				code = "unknown"
			}
			out = append(out, code)
		} else if o.kind == "fork" {
			out = append(out, res.out1+"|"+res.out2)
		} else {
			out = append(out, res.out1)
		}
	}
	return out
}

// pickName returns the name of a live replica, or "" if there are none.
func pickName(rng *rand.Rand, names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[rng.Intn(len(names))]
}

// genOps builds a random single-seed scenario. Roughly 10% of steps are
// deliberately invalid (empty names, unknown names, duplicate children) to
// exercise rejection paths against the naive registry.
func genOps(rng *rand.Rand, n int) []op {
	ops := []op{{kind: "seed", a: "s0"}}
	live := []string{"s0"}
	next := 1
	badName := func() string {
		used := map[string]bool{}
		for _, name := range live {
			used[name] = true
		}
		for {
			cand := "ghost" + strconv.Itoa(rng.Intn(1000))
			if !used[cand] {
				return cand
			}
		}
	}
	for len(ops) < n {
		if rng.Intn(10) == 0 {
			switch rng.Intn(4) {
			case 0:
				ops = append(ops, op{kind: "seed", a: ""})
			case 1:
				ops = append(ops, op{kind: "event", a: badName()})
			case 2:
				ops = append(ops, op{kind: "fork", a: pickName(rng, live), b: ""})
			case 3:
				ops = append(ops, op{kind: "join", a: pickName(rng, live), b: badName()})
			}
			continue
		}
		switch rng.Intn(7) {
		case 0, 1:
			ops = append(ops, op{kind: "event", a: pickName(rng, live)})
		case 2:
			parent := pickName(rng, live)
			child := "n" + strconv.Itoa(next)
			next++
			if rng.Intn(12) == 0 && len(live) > 1 {
				child = live[rng.Intn(len(live))]
			}
			ops = append(ops, op{kind: "fork", a: parent, b: child})
			if child != "" {
				found := false
				for _, name := range live {
					if name == child {
						found = true
					}
				}
				if !found {
					live = append(live, child)
				}
			}
		case 3:
			if len(live) >= 2 {
				i := rng.Intn(len(live))
				j := rng.Intn(len(live) - 1)
				if j >= i {
					j++
				}
				ops = append(ops, op{kind: "join", a: live[i], b: live[j]})
				live = append(live[:j], live[j+1:]...)
			} else {
				ops = append(ops, op{kind: "event", a: pickName(rng, live)})
			}
		case 4:
			ops = append(ops, op{kind: "peek", a: pickName(rng, live)})
		case 5:
			a := pickName(rng, live)
			b := a
			if len(live) > 1 && rng.Intn(3) != 0 {
				b = pickName(rng, live)
			}
			ops = append(ops, op{kind: "compare", a: a, b: b})
		default:
			ops = append(ops, op{kind: "string", a: pickName(rng, live)})
		}
	}
	return ops
}

// TestRandomDifferential runs 2000 random Fork/Event/Join sequences against
// both the production registry and the naive reference implementation and
// compares outputs character for character, plus structural invariants.
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 2000; trial++ {
		ops := genOps(rng, 12+rng.Intn(25))
		got := runReplay(t, ops)
		want := runNaive(ops)
		if len(got) != len(want) {
			t.Fatalf("trial %d output length mismatch", trial)
		}
		var log strings.Builder
		for i, o := range ops {
			desc := fmt.Sprintf("%s(%q,%q)", o.kind, o.a, o.b)
			verdict := "MATCH"
			if got[i] != want[i] {
				verdict = "MISMATCH"
			}
			fmt.Fprintf(&log, "trial=%d step=%d input=%s output=%q naive=%q judge=%s\n",
				trial, i, desc, got[i], want[i], verdict)
			if got[i] != want[i] {
				t.Fatalf("trial %d step %d %s: got %q naive %q\n%s", trial, i, desc, got[i], want[i], log.String())
			}
		}
		if trial < 5 || rng.Intn(400) == 0 {
			t.Logf("trial %d:\n%s", trial, log.String())
		}

		// Rebuild via the public API and verify structural invariants on
		// surviving replicas: every normalized event node has at least one
		// child subtree with minimum 0.
		r := NewRegistry()
		names := map[string]bool{}
		replayForInvariants(t, r, ops, names)
		for name := range names {
			s, err := r.String(name)
			if err != nil {
				continue
			}
			if strings.Contains(s, ";;") {
				t.Fatalf("bad stamp %q", s)
			}
		}
		assertEventNormalization(t, ops, trial)
		assertIdentityInvariant(t, r, trial)
	}
}

func replayForInvariants(t *testing.T, r *Registry, ops []op, names map[string]bool) {
	t.Helper()
	for _, o := range ops {
		switch o.kind {
		case "seed":
			if err := r.Seed(o.a); err == nil {
				names[o.a] = true
			}
		case "fork":
			if _, _, err := r.Fork(o.a, o.b); err == nil {
				names[o.b] = true
			}
		case "join":
			if _, err := r.Join(o.a, o.b); err == nil {
				delete(names, o.b)
			}
		}
	}
}

// assertEventNormalization walks every surviving internal event tree and
// checks that each node has min(left)==0 or min(right)==0.
func assertEventNormalization(t *testing.T, ops []op, trial int) {
	t.Helper()
	r := NewRegistry()
	seen := map[string]bool{}
	for _, o := range ops {
		switch o.kind {
		case "seed":
			if err := r.Seed(o.a); err == nil {
				seen[o.a] = true
			}
		case "fork":
			if _, _, err := r.Fork(o.a, o.b); err == nil {
				seen[o.b] = true
			}
		case "event":
			_ = r.Event(o.a)
		case "join":
			if _, err := r.Join(o.a, o.b); err == nil {
				delete(seen, o.b)
			}
		}
		r.mu.Lock()
		for name, s := range r.replicas {
			checkEventNorm(t, s.event, fmt.Sprintf("trial %d replica %s", trial, name))
		}
		r.mu.Unlock()
	}
}

func checkEventNorm(t *testing.T, e Event, where string) {
	t.Helper()
	if e.kind == 0 {
		return
	}
	if evMin(*e.left) != 0 && evMin(*e.right) != 0 {
		t.Fatalf("%s: node %s has no zero-minimum child", where, e)
	}
	checkEventNorm(t, *e.left, where)
	checkEventNorm(t, *e.right, where)
}

// assertIdentityInvariant sums all surviving production identities and checks
// the result is exactly 1 (pairwise disjoint + complete cover).
func assertIdentityInvariant(t *testing.T, r *Registry, trial int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var acc ID = idZero()
	for _, s := range r.replicas {
		sum, ok := idSum(acc, s.id)
		if !ok {
			t.Fatalf("trial %d: overlapping surviving identities", trial)
		}
		acc = sum
	}
	if len(r.replicas) > 0 && acc.String() != "1" {
		t.Fatalf("trial %d: surviving identities sum to %s not 1", trial, acc)
	}
}

// TestReplayDeterministic replays the same recorded sequence twice and checks
// byte-identical output.
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for trial := 0; trial < 50; trial++ {
		ops := genOps(rng, 30)
		first := runReplay(t, ops)
		second := runReplay(t, ops)
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("trial %d step %d non-deterministic: %q vs %q", trial, i, first[i], second[i])
			}
		}
	}
}

// TestConcurrent drives many goroutines at one registry; run with -race to
// verify serializability of the locking implementation.
func TestConcurrent(t *testing.T) {
	r := NewRegistry()
	if err := r.Seed("root"); err != nil {
		t.Fatal(err)
	}
	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			child := "c" + strconv.Itoa(id)
			_, _, _ = r.Fork("root", child)
			for k := 0; k < 50; k++ {
				_ = r.Event(child)
				_, _ = r.Peek(child)
				_, _ = r.Compare(child, "root")
			}
		}(w)
	}
	wg.Wait()
	// All children join back without overlap; the summed identity must be 1.
	for w := 0; w < workers; w++ {
		_, _ = r.Join("root", "c"+strconv.Itoa(w))
	}
	got := mustString(t, r, "root")
	if !strings.HasPrefix(got, "(1;") {
		t.Fatalf("after concurrent fork/event/join: %s", got)
	}
	t.Logf("concurrent result: %s (basis: disjoint children joined back to identity 1)", got)
}
