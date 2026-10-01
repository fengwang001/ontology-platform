package rename

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type caseSpec struct {
	deleted   []testFile
	added     []testFile
	threshold int
}

func randomSpec(rng *rand.Rand) caseSpec {
	// A small pool of lines; sometimes a very long line is included to
	// exercise the size-ratio pruning.
	pool := []string{"a\n", "b\n", "c\n", "x\n", "y\n", "z\n", "\n", "a", "b"}
	longLine := strings.Repeat("L", 40+rng.Intn(80)) + "\n"

	build := func(prefix string, n int) []testFile {
		files := make([]testFile, n)
		used := map[string]bool{}
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("%s/%s%02d", prefix, pick(rng, []string{"f", "g", "h"}), i)
			for used[path] {
				path += "x"
			}
			used[path] = true
			var b strings.Builder
			lines := rng.Intn(5)
			for j := 0; j < lines; j++ {
				b.WriteString(pick(rng, pool))
			}
			if rng.Intn(5) == 0 {
				b.WriteString(longLine)
			}
			if rng.Intn(8) == 0 {
				files[i] = testFile{path, ""}
			} else {
				files[i] = testFile{path, b.String()}
			}
		}
		return files
	}

	// Occasionally reuse names across sides to test the basename tiebreak.
	dels := build("d", rng.Intn(5))
	adds := build("a", rng.Intn(5))
	if rng.Intn(2) == 0 && len(adds) > 0 && len(dels) > 0 {
		// Give one add the same basename as a delete.
		adds[0].path = "a/" + baseName(dels[0].path)
	}
	// Occasionally duplicate contents across the two sides.
	if rng.Intn(3) == 0 && len(dels) > 0 && len(adds) > 0 {
		adds[0].content = dels[0].content
	}

	threshold := 1 + rng.Intn(100)
	return caseSpec{dels, adds, threshold}
}

func pick(rng *rand.Rand, xs []string) string {
	return xs[rng.Intn(len(xs))]
}

func runSpec(spec caseSpec) (*Detector, *Result) {
	d, _ := New(spec.threshold, len(spec.deleted)+len(spec.added)+1)
	for _, f := range spec.deleted {
		if err := d.RegisterDeleted(f.path, []byte(f.content)); err != nil {
			panic(err)
		}
	}
	for _, f := range spec.added {
		if err := d.RegisterAdded(f.path, []byte(f.content)); err != nil {
			panic(err)
		}
	}
	return d, d.Detect()
}

func describeFiles(files []testFile) string {
	var parts []string
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%s=%q(%dB)", f.path, f.content, len(f.content)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(1052))
	for i := 0; i < cases; i++ {
		spec := randomSpec(rng)
		delMap := map[string]string{}
		addMap := map[string]string{}
		for _, f := range spec.deleted {
			delMap[f.path] = f.content
		}
		for _, f := range spec.added {
			addMap[f.path] = f.content
		}

		d, got := runSpec(spec)
		want, cComputedNaive, pruned := naiveDetect(delMap, addMap, spec.threshold)

		t.Logf(
			"case %d T=%d deleted=%s added=%s -> renames=%v unpairedDeleted=%v unpairedAdded=%v; "+
				"decision basis: naive C computations=%d, bound-pruned pairs=%d, real C computations=%d",
			i, spec.threshold,
			describeFiles(spec.deleted), describeFiles(spec.added),
			got.Renames, got.UnpairedDeleted, got.UnpairedAdded,
			cComputedNaive, pruned, d.commonComputedCount(),
		)

		if !reflect.DeepEqual(*got, want) {
			t.Fatalf("case %d mismatch:\n got  %+v\n want %+v\n input: deleted=%s added=%s",
				i, *got, want, describeFiles(spec.deleted), describeFiles(spec.added))
		}
		// Pruning proof: C must be computed exactly on the pairs the bound
		// did not prune (among phase-2 survivor combinations).
		survivorCombos := cComputedNaive
		if survivorCombos > 0 {
			if got := d.commonComputedCount(); got != survivorCombos-pruned {
				t.Fatalf("case %d: C computed %d times, want %d (combos=%d pruned=%d)",
					i, got, survivorCombos-pruned, survivorCombos, pruned)
			}
		}

		// Order-independence proof: register in reverse order too.
		d2, _ := New(spec.threshold, len(spec.deleted)+len(spec.added)+1)
		for j := len(spec.added) - 1; j >= 0; j-- {
			f := spec.added[j]
			if err := d2.RegisterAdded(f.path, []byte(f.content)); err != nil {
				t.Fatal(err)
			}
		}
		for j := len(spec.deleted) - 1; j >= 0; j-- {
			f := spec.deleted[j]
			if err := d2.RegisterDeleted(f.path, []byte(f.content)); err != nil {
				t.Fatal(err)
			}
		}
		if got2 := d2.Detect(); !reflect.DeepEqual(*got2, want) {
			t.Fatalf("case %d order dependence:\n fwd %+v\n rev %+v", i, *got, *got2)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	const n = 24
	d, _ := New(40, 2*n)

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*Result, 100)

	// Half the goroutines register files, half call Detect early (which may
	// freeze the session and reject some registrations).
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			content := fmt.Sprintf("line-%d\nshared\n", i)
			path := fmt.Sprintf("d/p%02d", i)
			_ = d.RegisterDeleted(path, []byte(content))
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			content := fmt.Sprintf("line-%d\nshared\n", i)
			path := fmt.Sprintf("a/p%02d", i)
			_ = d.RegisterAdded(path, []byte(content))
		}()
	}
	for i := range results {
		i := i
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = d.Detect()
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent Detect returned different result pointers/values: %+v vs %+v",
				results[i], results[0])
		}
	}
	if runs := d.detectRunCount(); runs != 1 {
		t.Fatalf("algorithm ran %d times under concurrent Detect, want 1", runs)
	}
}
