package pretty

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentRenderAndRegister hammers a session with parallel renders
// at different widths plus concurrent registrations. Run with -race.
func TestConcurrentRenderAndRegister(t *testing.T) {
	s := NewSession()
	mustRegister(t, s, "base", Group(Seq(Text("x"), BreakableSpace(), Text("y"))))
	doc := Seq(Text("["), Ref("base"), Text("]"))
	// Expected outputs per width, computed up front from a single render.
	want := map[int]string{}
	for w := 1; w <= 8; w++ {
		out, _, err := s.Render(doc, w)
		if err != nil {
			t.Fatalf("render width %d: %v", w, err)
		}
		want[w] = out
	}
	var wg sync.WaitGroup
	for w := 1; w <= 8; w++ {
		wg.Add(1)
		go func(width int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				out, _, err := s.Render(doc, width)
				if err != nil {
					t.Errorf("render width %d: %v", width, err)
					return
				}
				if out != want[width] {
					t.Errorf("width %d: got %q, want %q", width, out, want[width])
					return
				}
			}
		}(w)
	}
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				// Unique names: every registration is accepted exactly once.
				if err := s.Register(fmt.Sprintf("f%d-%d", k, i), Text("v")); err != nil {
					t.Errorf("register: %v", err)
					return
				}
			}
		}(k)
	}
	wg.Wait()
}

// TestFitsScanLinearInDepth proves that deeply nested groups do not cause
// quadratic scan work: each group's fits check collapses the undecided
// inner groups through precomputed annotations.
func TestFitsScanLinearInDepth(t *testing.T) {
	for _, depth := range []int{100, 200, 400, 800} {
		doc := Doc(Text("x"))
		for i := 0; i < depth; i++ {
			doc = Group(doc)
		}
		fitsProbe.Store(0)
		out, _, err := NewSession().Render(doc, 10)
		if err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		if out != "x" {
			t.Fatalf("depth %d: out = %q", depth, out)
		}
		steps := fitsProbe.Load()
		// Each of the depth groups scans O(1) nodes: the inner group is
		// collapsed via its annotation. A quadratic scan would need
		// ~depth^2/2 steps.
		if limit := int64(4*depth + 16); steps > limit {
			t.Fatalf("depth %d: fits steps = %d, want <= %d (quadratic?)", depth, steps, limit)
		}
		t.Logf("depth %d: fits steps = %d", depth, steps)
	}
}

// TestFitsScanLinearInSize proves near-linear total cost on a large wide
// document: groups separated by top-level breakables truncate each other's
// fits scan.
func TestFitsScanLinearInSize(t *testing.T) {
	const n = 50000
	parts := make([]Doc, 0, 2*n)
	for i := 0; i < n; i++ {
		parts = append(parts, Group(Text("ab")), BreakableSpace())
	}
	doc := Seq(parts...)
	fitsProbe.Store(0)
	out, _, err := NewSession().Render(doc, 10)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := strings.Count(out, "\n"); got != n {
		t.Fatalf("newlines = %d, want %d", got, n)
	}
	steps := fitsProbe.Load()
	if limit := int64(10 * n); steps > limit {
		t.Fatalf("fits steps = %d, want <= %d", steps, limit)
	}
	t.Logf("n=%d: fits steps = %d", n, steps)
}

// TestDeepNestingNoStackOverflow renders the deepest legal document and
// rejects the deepest illegal one without crashing.
func TestDeepNestingNoStackOverflow(t *testing.T) {
	doc := Doc(Text("leaf"))
	for i := 0; i < 499; i++ {
		doc = Group(Indent(1, doc))
	}
	doc = Group(doc) // depth: 499*2 + 1 + 1 = 1000
	if _, _, err := NewSession().Render(doc, 5); err != nil {
		t.Fatalf("depth 1000 should render: %v", err)
	}
	doc = Indent(0, doc) // depth 1001
	if _, _, err := NewSession().Render(doc, 5); err == nil {
		t.Fatalf("depth 1001 should fail")
	}
}

func BenchmarkRenderWideDocument(b *testing.B) {
	const n = 20000
	parts := make([]Doc, 0, 3*n)
	for i := 0; i < n; i++ {
		parts = append(parts,
			Group(Seq(Text("key"), BreakableSpace(), Text("value"))),
			BreakableSpace(),
			Text(";"))
	}
	doc := Seq(parts...)
	s := NewSession()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Render(doc, 80); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDeepGroups(b *testing.B) {
	doc := Doc(Text("x"))
	for i := 0; i < 499; i++ { // depth 999, the deepest legal shape
		doc = Group(Indent(1, doc))
	}
	s := NewSession()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Render(doc, 10); err != nil {
			b.Fatal(err)
		}
	}
}
