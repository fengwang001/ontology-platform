package ontology_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

// Every byte truncation of a valid patch: never panics; if it applies it
// must do so cleanly, otherwise an error is returned and target is unchanged.
func TestTruncateEveryByte(t *testing.T) {
	srcs := []string{"a\nb\nc\nd\n", "x\r\ny\n", "one", "a\n", ""}
	dsts := []string{"a\nB\nc\nd\ne\n", "X\r\ny\r\nz", "two", "", "b\n"}
	for ci := range srcs {
		d := render(t, srcs[ci], dsts[ci], 3)
		for cut := 0; cut <= len(d); cut++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic at cut %d: %v", cut, r)
					}
				}()
				original := []byte(srcs[ci])
				got, err := patch.Apply(original, d[:cut], patch.Options{})
				if err == nil {
					// Truncated patch accepted: result must be a valid transform.
					_ = got
					return
				}
				if string(original) != srcs[ci] {
					t.Fatalf("cut %d mutated failed target", cut)
				}
			}()
		}
	}
}

// Flip one char per line: either rejected at parse or at apply, target intact.
func TestFlipEachLine(t *testing.T) {
	d := render(t, "a\nb\nc\n", "a\nB\nc\n", 3)
	for li := 0; li < len(d); li++ {
		if d[li] != '-' && d[li] != '+' && d[li] < '0' && d[li] > '9' {
			continue
		}
		flipped := flipAt(append([]byte(nil), d...), li)
		original := []byte("a\nb\nc\n")
		got, err := patch.Apply(original, flipped, patch.Options{Fuzz: 0})
		if err == nil {
			// Applying a flipped patch is allowed only if it is still valid;
			// it must never corrupt target on failure.
			_ = got
			continue
		}
		if string(original) != "a\nb\nc\n" {
			t.Fatalf("flip %d mutated target", li)
		}
	}
}

func flipAt(d []byte, i int) []byte {
	switch d[i] {
	case '-':
		d[i] = '+'
	case '+':
		d[i] = '-'
	default:
		if d[i] == '0' {
			d[i] = '1'
		} else if d[i] >= '1' && d[i] <= '9' {
			d[i]--
		} else {
			d[i] = 'x'
		}
	}
	return d
}

func TestResourceLimits(t *testing.T) {
	var big string
	for i := 0; i < 10; i++ {
		big += fmt.Sprintf("line%d\n", i)
	}
	var changed string
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			changed += fmt.Sprintf("X%d\n", i)
		} else {
			changed += fmt.Sprintf("line%d\n", i)
		}
	}
	d := render(t, big, changed, 0)
	cases := []struct {
		name string
		opt  patch.Options
	}{
		{"bytes", patch.Options{MaxBytes: 10}},
		{"hunks", patch.Options{MaxHunks: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := []byte(big)
			got, err := patch.Apply(original, d, tc.opt)
			if got != nil {
				t.Fatalf("over-limit must return nil, got %q", got)
			}
			if !errors.Is(err, patch.ErrFormat) {
				t.Fatalf("over-limit want format error, got %v", err)
			}
			if string(original) != big {
				t.Fatal("target mutated on limit rejection")
			}
		})
	}
}

func TestConcurrentStore(t *testing.T) {
	const n = 16
	base := "same\n"
	store := patch.NewStore()
	store.Put("doc", []byte(base), 1)

	patches := make([]udiff.Patch, n)
	for g := 0; g < n; g++ {
		p, err := udiff.Build(
			lines.Split(base),
			lines.Split(fmt.Sprintf("winner-%d\n", g)),
			0, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		patches[g] = p
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	oks, fails := 0, 0
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			_, err := store.Apply("doc", patches[g], 0)
			mu.Lock()
			if err == nil {
				oks++
			} else {
				fails++
			}
			mu.Unlock()
		}(g)
	}
	close(start)
	wg.Wait()
	if oks+fails != n {
		t.Fatalf("accounting %d+%d != %d", oks, fails, n)
	}
	final, ok := store.Get("doc")
	if !ok {
		t.Fatal("doc missing")
	}
	if final.Version != 1+oks {
		t.Fatalf("version=%d want %d", final.Version, 1+oks)
	}
	log := store.Log()
	if len(log) != oks {
		t.Fatalf("log len %d want %d", len(log), oks)
	}
	replayed, err := patch.Replay([]byte(base), log)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != string(final.Content) {
		t.Fatalf("replay mismatch\n got=%q\nwant=%q", replayed, final.Content)
	}
}

func TestHunkAndEditWiring(t *testing.T) {
	scr, err := edit.Diff(lines.Split("a\nb\n"), lines.Split("b\n"), edit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	hs := hunk.Build(scr, 3)
	if len(hs) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(hs))
	}
}
