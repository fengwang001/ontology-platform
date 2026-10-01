package rename

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustDetector(t *testing.T, threshold, capacity int) *Detector {
	t.Helper()
	detector, err := NewDetector(threshold, capacity)
	if err != nil {
		t.Fatalf("NewDetector(%d,%d) unexpected error: %v", threshold, capacity, err)
	}
	return detector
}

func regDel(t *testing.T, d *Detector, path, content string) {
	t.Helper()
	if err := d.RegisterDeleted(path, []byte(content)); err != nil {
		t.Fatalf("RegisterDeleted(%q) unexpected error: %v", path, err)
	}
}

func regAdd(t *testing.T, d *Detector, path, content string) {
	t.Helper()
	if err := d.RegisterAdded(path, []byte(content)); err != nil {
		t.Fatalf("RegisterAdded(%q) unexpected error: %v", path, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertRenames(t *testing.T, result *Result, want []Rename) {
	t.Helper()
	if len(result.Renames) != len(want) {
		t.Fatalf("renames len: got %+v want %+v", result.Renames, want)
	}
	for i := range want {
		if result.Renames[i] != want[i] {
			t.Fatalf("rename[%d]: got %+v want %+v (full %+v)", i, result.Renames[i], want[i], result.Renames)
		}
	}
}

func TestConstructorValidationPrecedence(t *testing.T) {
	if _, err := NewDetector(0, 0); !errors.Is(err, ErrThresholdOutOfRange) {
		t.Fatalf("threshold precedence: got %v want %v", err, ErrThresholdOutOfRange)
	}
	if _, err := NewDetector(101, 0); !errors.Is(err, ErrThresholdOutOfRange) {
		t.Fatalf("threshold upper bound: got %v", err)
	}
	if _, err := NewDetector(50, 0); !errors.Is(err, ErrCapOutOfRange) {
		t.Fatalf("cap lower bound: got %v want %v", err, ErrCapOutOfRange)
	}
}

func TestEmptyDetect(t *testing.T) {
	result := mustDetector(t, 50, 10).Detect()
	if len(result.Renames) != 0 || len(result.UnpairedDeleted) != 0 || len(result.UnpairedAdded) != 0 {
		t.Fatalf("empty detect: %+v", result)
	}
	if result.Renames == nil || result.UnpairedDeleted == nil || result.UnpairedAdded == nil {
		t.Fatalf("result slices must be non-nil: %+v", result)
	}
}

func TestRegisterErrorPrecedence(t *testing.T) {
	d := mustDetector(t, 50, 2)
	regDel(t, d, "a", "x")
	regAdd(t, d, "b", "y")

	if err := d.RegisterDeleted("", nil); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty beats cap: got %v want %v", err, ErrEmptyPath)
	}
	if err := d.RegisterDeleted("c", nil); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("cap exceeded: got %v want %v", err, ErrCapacityExceeded)
	}

	d.Detect()
	if err := d.RegisterDeleted("", nil); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen beats empty: got %v want %v", err, ErrFrozen)
	}
	if err := d.RegisterDeleted("a", nil); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen beats duplicate: got %v want %v", err, ErrFrozen)
	}
	if err := d.RegisterDeleted("c", nil); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen beats cap: got %v want %v", err, ErrFrozen)
	}

	// Before freeze duplicate (including cross-side) beats cap; rejected
	// registrations must not consume capacity.
	d2 := mustDetector(t, 50, 2)
	regDel(t, d2, "dup", "x")
	if err := d2.RegisterAdded("dup", []byte("z")); !errors.Is(err, ErrPathExists) {
		t.Fatalf("cross-side duplicate: got %v want %v", err, ErrPathExists)
	}
	regAdd(t, d2, "ok", "y")
	if err := d2.RegisterAdded("ok", []byte("z")); !errors.Is(err, ErrPathExists) {
		t.Fatalf("same-side duplicate: got %v want %v", err, ErrPathExists)
	}
	if err := d2.RegisterAdded("new", []byte("z")); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("cap after rejected duplicates still 2: got %v", err)
	}
}

func TestExactSameBasenameFirst(t *testing.T) {
	// One identical content, multiple files on both sides: same-basename
	// pairs must form before cross-basename leftovers.
	d := mustDetector(t, 50, 10)
	regDel(t, d, "dir1/name_a", "same-content")
	regDel(t, d, "dir2/name_b", "same-content")
	regAdd(t, d, "dir3/name_b", "same-content")
	regAdd(t, d, "dir4/name_a", "same-content")

	result := d.Detect()
	want := []Rename{
		{Source: "dir1/name_a", Target: "dir4/name_a", Score: 100},
		{Source: "dir2/name_b", Target: "dir3/name_b", Score: 100},
	}
	assertRenames(t, result, want)
}

func TestExactSubGroupPathOrderAndLeftovers(t *testing.T) {
	d := mustDetector(t, 50, 20)
	content := []byte("C")
	// Basename "f": 3 deletes, 2 adds; two smallest delete paths pair first.
	for _, p := range []string{"z/f", "a/f", "m/f"} {
		if err := d.RegisterDeleted(p, content); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"x/f", "b/f"} {
		if err := d.RegisterAdded(p, content); err != nil {
			t.Fatal(err)
		}
	}
	// Leftover delete then pairs cross-basename with the remaining add.
	regAdd(t, d, "g", "C")

	result := d.Detect()
	want := []Rename{
		{Source: "a/f", Target: "b/f", Score: 100},
		{Source: "m/f", Target: "x/f", Score: 100},
		{Source: "z/f", Target: "g", Score: 100},
	}
	assertRenames(t, result, want)
	if len(result.UnpairedDeleted) != 0 || len(result.UnpairedAdded) != 0 {
		t.Fatalf("expected no unpaired, got %+v", result)
	}
}

// tenLineFile builds a 20-byte file with 10 two-byte lines. The first
// sharedCount lines are "0\n".."9\n" prefix lines; the rest are distinct
// letter lines ("a\n".."j\n") so they never collide with digit lines.
func tenLineFile(sharedCount int) string {
	var builder strings.Builder
	for i := 0; i < 10; i++ {
		if i < sharedCount {
			builder.WriteString(fmt.Sprintf("%d\n", i))
		} else {
			builder.WriteString(fmt.Sprintf("%c\n", 'a'+i))
		}
	}
	out := builder.String()
	if len(out) != 20 {
		panic(fmt.Sprintf("test helper produced %d bytes, want 20", len(out)))
	}
	return out
}

func TestScoreEqualThresholdAndBelow(t *testing.T) {
	del := tenLineFile(10)

	// T=50: 5 shared lines = 10 shared bytes -> floor(10*100/20)=50, kept;
	// 4 shared lines -> floor(8*100/20)=40, dropped.
	d := mustDetector(t, 50, 10)
	regDel(t, d, "del", del)
	regAdd(t, d, "keep", tenLineFile(5))
	regAdd(t, d, "drop", tenLineFile(4))
	result := d.Detect()
	assertRenames(t, result, []Rename{{Source: "del", Target: "keep", Score: 50}})
	if !equalStrings(result.UnpairedAdded, []string{"drop"}) {
		t.Fatalf("unpaired adds: %v", result.UnpairedAdded)
	}

	// T=51: the same 50-score combination now falls below the threshold.
	d2 := mustDetector(t, 51, 10)
	regDel(t, d2, "del", del)
	regAdd(t, d2, "keep", tenLineFile(5))
	if result2 := d2.Detect(); len(result2.Renames) != 0 {
		t.Fatalf("score 50 with T=51 must not pair: %+v", result2.Renames)
	}
}

func TestFloor99(t *testing.T) {
	// floor(999*100/1000) = 99, never rounded to 100.
	del := strings.Repeat("a\n", 500) // 1000 bytes
	add := strings.Repeat("a\n", 499) + "b\n"
	if len(del) != 1000 || len(add) != 1000 {
		t.Fatalf("setup sizes: %d %d", len(del), len(add))
	}
	d := mustDetector(t, 99, 10)
	regDel(t, d, "f", del)
	regAdd(t, d, "g", add)
	assertRenames(t, d.Detect(), []Rename{{Source: "f", Target: "g", Score: 99}})

	d2 := mustDetector(t, 100, 10)
	regDel(t, d2, "f", del)
	regAdd(t, d2, "g", add)
	if result := d2.Detect(); len(result.Renames) != 0 {
		t.Fatalf("99 must not pass T=100: %+v", result.Renames)
	}
}

func TestTrailingNewlineIsDistinctLine(t *testing.T) {
	// "a\n" and "a" share zero bytes: no exact match, similarity 0.
	d := mustDetector(t, 50, 10)
	regDel(t, d, "f", "a\n")
	regAdd(t, d, "g", "a")
	result := d.Detect()
	if len(result.Renames) != 0 {
		t.Fatalf("trailing-newline variants paired: %+v", result.Renames)
	}
	if !equalStrings(result.UnpairedDeleted, []string{"f"}) ||
		!equalStrings(result.UnpairedAdded, []string{"g"}) {
		t.Fatalf("unpaired mismatch: %+v", result)
	}

	// Empty content is zero lines and matches another empty file exactly.
	d2 := mustDetector(t, 50, 10)
	regDel(t, d2, "e1", "")
	regAdd(t, d2, "e2", "")
	assertRenames(t, d2.Detect(), []Rename{{Source: "e1", Target: "e2", Score: 100}})

	// Empty vs non-empty is pruned by the size bound before computing C.
	d3 := mustDetector(t, 1, 10)
	regDel(t, d3, "empty", "")
	regAdd(t, d3, "nonempty", "x")
	d3.Detect()
	if d3.commonBytesCalls != 0 {
		t.Fatalf("empty pair must be pruned without computing C, calls=%d", d3.commonBytesCalls)
	}
}

func TestTwoAddsCompeteHigherScoreWins(t *testing.T) {
	// Uniform 2-byte lines: shared lines are digits, private lines letters.
	mk := func(shared int, priv []byte) string {
		var builder strings.Builder
		for i := 0; i < 10; i++ {
			if i < shared {
				builder.WriteString(fmt.Sprintf("%d\n", i))
			} else {
				builder.WriteByte(priv[i-shared])
				builder.WriteByte('\n')
			}
		}
		return builder.String()
	}
	del := mk(10, nil)
	high := mk(7, []byte("abc"))  // 7 shared lines -> 14/20 = 70
	low := mk(5, []byte("abcde")) // 5 shared lines -> 10/20 = 50

	d := mustDetector(t, 50, 10)
	regDel(t, d, "d/x", del)
	regAdd(t, d, "a/low", low)
	regAdd(t, d, "b/high", high)
	result := d.Detect()
	assertRenames(t, result, []Rename{{Source: "d/x", Target: "b/high", Score: 70}})
	if !equalStrings(result.UnpairedAdded, []string{"a/low"}) {
		t.Fatalf("loser should be unpaired: %v", result.UnpairedAdded)
	}
}

func TestTieSameBasenameFirst(t *testing.T) {
	mk := func(shared int, priv []byte) string {
		var builder strings.Builder
		for i := 0; i < 10; i++ {
			if i < shared {
				builder.WriteString(fmt.Sprintf("%d\n", i))
			} else {
				builder.WriteByte(priv[i-shared])
				builder.WriteByte('\n')
			}
		}
		return builder.String()
	}
	del := mk(10, nil)
	diffName := mk(5, []byte("abcde")) // basename z, score 50
	// Same five digit lines in a different order; C is count-based, so the
	// multiset intersection is still exactly 10 bytes (score 50).
	sameName := "4\n3\n2\n1\n0\nf\ng\nh\ni\nj\n"

	if got := commonBytes([]byte(del), []byte(sameName)) * 100 / 20; got != 50 {
		t.Fatalf("sameName setup score = %d, want 50", got)
	}
	if got := commonBytes([]byte(del), []byte(diffName)) * 100 / 20; got != 50 {
		t.Fatalf("diffName setup score = %d, want 50", got)
	}

	d := mustDetector(t, 50, 10)
	regDel(t, d, "dir/x", del)
	regAdd(t, d, "a/z", diffName)
	regAdd(t, d, "b/x", sameName)
	result := d.Detect()
	assertRenames(t, result, []Rename{{Source: "dir/x", Target: "b/x", Score: 50}})
	if !equalStrings(result.UnpairedAdded, []string{"a/z"}) {
		t.Fatalf("different-basename loser should be unpaired: %v", result.UnpairedAdded)
	}
}

func TestPruningCounter(t *testing.T) {
	// Files reach phase two (all contents distinct).
	// Sizes: deletes 100, 500; adds 100, 600, 10000.
	// T=50: bound floor(min*100/max):
	//   100 vs 100 ->100 kept; 100 vs 600 ->16 pruned; 100 vs 10000 ->0 pruned
	//   500 vs 100 ->20 pruned; 500 vs 600 ->83 kept; 500 vs 10000 ->5 pruned
	// So exactly 2 combinations are evaluated (C computed); 4 pruned.
	d := mustDetector(t, 50, 10)
	regDel(t, d, "d100", strings.Repeat("d\n", 50))
	regDel(t, d, "d500", strings.Repeat("e\n", 250))
	regAdd(t, d, "a100", strings.Repeat("f\n", 50))
	regAdd(t, d, "a600", strings.Repeat("g\n", 300))
	regAdd(t, d, "a10000", strings.Repeat("h\n", 5000))

	result := d.Detect()
	if d.commonBytesCalls != 2 {
		t.Fatalf("commonBytesCalls = %d, want exactly 2 unpruned combinations", d.commonBytesCalls)
	}
	// No actual content lines are shared, so nothing pairs even though both
	// evaluated combinations survive the size bound.
	if len(result.Renames) != 0 {
		t.Fatalf("unexpected renames: %+v", result.Renames)
	}
}

func TestFrozenRejectsAndComputeOnce(t *testing.T) {
	d := mustDetector(t, 50, 10)
	regDel(t, d, "f", "hello\n")
	regAdd(t, d, "g", "hello\n")

	first := d.Detect()
	if !errors.Is(d.RegisterAdded("after", []byte("x")), ErrFrozen) {
		t.Fatalf("register after Detect must be ErrFrozen")
	}
	if !errors.Is(d.RegisterDeleted("after2", []byte("x")), ErrFrozen) {
		t.Fatalf("delete after Detect must be ErrFrozen")
	}

	second := d.Detect()
	third := d.Detect()
	if first != second || second != third {
		t.Fatalf("repeated Detect must return the same *Result")
	}
	if d.computeCalls != 1 {
		t.Fatalf("computeCalls = %d, want 1 (result computed exactly once)", d.computeCalls)
	}
	if d.detectCalls != 3 {
		t.Fatalf("detectCalls = %d, want 3 invocations counted", d.detectCalls)
	}
}

func TestConcurrentDetectAndRegister(t *testing.T) {
	d := mustDetector(t, 50, 64)

	var registerGroup sync.WaitGroup
	for i := 0; i < 32; i++ {
		registerGroup.Add(1)
		go func(i int) {
			defer registerGroup.Done()
			content := fmt.Sprintf("content-%d\n", i%8)
			if err := d.RegisterDeleted(fmt.Sprintf("del/%02d", i), []byte(content)); err != nil {
				t.Errorf("concurrent delete register: %v", err)
			}
		}(i)
	}
	for i := 0; i < 32; i++ {
		registerGroup.Add(1)
		go func(i int) {
			defer registerGroup.Done()
			content := fmt.Sprintf("content-%d\n", i%8)
			if err := d.RegisterAdded(fmt.Sprintf("add/%02d", i), []byte(content)); err != nil {
				t.Errorf("concurrent add register: %v", err)
			}
		}(i)
	}
	registerGroup.Wait()

	const detectGoroutines = 16
	var detectGroup sync.WaitGroup
	results := make([]*Result, detectGoroutines)
	start := make(chan struct{})
	for i := 0; i < detectGoroutines; i++ {
		detectGroup.Add(1)
		go func(i int) {
			defer detectGroup.Done()
			<-start
			results[i] = d.Detect()
		}(i)
	}
	close(start)
	detectGroup.Wait()

	for i := 1; i < detectGoroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent Detect returned different *Result instances")
		}
	}
	if d.computeCalls != 1 {
		t.Fatalf("computeCalls = %d under concurrent Detect, want 1", d.computeCalls)
	}
	if d.detectCalls != detectGoroutines {
		t.Fatalf("detectCalls = %d, want %d", d.detectCalls, detectGoroutines)
	}
}
