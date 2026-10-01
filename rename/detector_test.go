package rename

import (
	"reflect"
	"strings"
	"testing"
)

func regOne(t *testing.T, d *Detector, deleted bool, path, content string) {
	t.Helper()
	var err error
	if deleted {
		err = d.RegisterDeleted(path, []byte(content))
	} else {
		err = d.RegisterAdded(path, []byte(content))
	}
	if err != nil {
		t.Fatalf("unexpected register error for %q: %v", path, err)
	}
}

func emptyResult() *Result {
	return &Result{Renames: []Rename{}, UnpairedDeleted: []string{}, UnpairedAdded: []string{}}
}

func TestEmptyDetect(t *testing.T) {
	d, err := New(50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Detect(); !reflect.DeepEqual(got, emptyResult()) {
		t.Fatalf("empty detect = %+v", got)
	}
}

func TestConstructorErrorPriority(t *testing.T) {
	if _, err := New(0, 0); err != ErrInvalidThreshold {
		t.Fatalf("got %v, want ErrInvalidThreshold", err)
	}
	if _, err := New(101, 0); err != ErrInvalidThreshold {
		t.Fatalf("got %v, want ErrInvalidThreshold", err)
	}
	if _, err := New(50, 0); err != ErrInvalidCapacity {
		t.Fatalf("got %v, want ErrInvalidCapacity", err)
	}
	if _, err := New(1, 100); err != nil {
		t.Fatalf("valid constructor failed: %v", err)
	}
}

func TestRegistrationErrorPriority(t *testing.T) {
	d, _ := New(50, 2)
	if err := d.RegisterDeleted("", nil); err != ErrEmptyPath {
		t.Fatalf("got %v, want ErrEmptyPath", err)
	}
	regOne(t, d, true, "a", "x")
	if err := d.RegisterAdded("a", nil); err != ErrPathExists {
		t.Fatalf("cross-side duplicate: got %v, want ErrPathExists", err)
	}
	regOne(t, d, true, "b", "y")
	if err := d.RegisterDeleted("", nil); err != ErrEmptyPath {
		t.Fatalf("got %v, want ErrEmptyPath", err)
	}
	if err := d.RegisterDeleted("a", nil); err != ErrPathExists {
		t.Fatalf("got %v, want ErrPathExists", err)
	}
	if err := d.RegisterDeleted("c", nil); err != ErrCapacityReached {
		t.Fatalf("got %v, want ErrCapacityReached", err)
	}
	d.Detect()
	if err := d.RegisterDeleted("", nil); err != ErrFrozen {
		t.Fatalf("got %v, want ErrFrozen", err)
	}
	if err := d.RegisterDeleted("brand-new", nil); err != ErrFrozen {
		t.Fatalf("got %v, want ErrFrozen", err)
	}
}

func TestRejectedRegistrationLeavesState(t *testing.T) {
	d, _ := New(50, 5)
	regOne(t, d, true, "a", "x")
	if err := d.RegisterAdded("a", []byte("y")); err != ErrPathExists {
		t.Fatalf("got %v", err)
	}
	want := &Result{
		Renames:         []Rename{},
		UnpairedDeleted: []string{"a"},
		UnpairedAdded:   []string{},
	}
	if got := d.Detect(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestExactMatchSameNameFirst(t *testing.T) {
	d, _ := New(50, 10)
	regOne(t, d, true, "d/a", "X")
	regOne(t, d, true, "d/c", "X")
	regOne(t, d, false, "e/c", "X")
	regOne(t, d, false, "e/a", "X")
	want := []Rename{
		{From: "d/a", To: "e/a", Score: 100},
		{From: "d/c", To: "e/c", Score: 100},
	}
	if got := d.Detect().Renames; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestExactMatchNameThenRemainder(t *testing.T) {
	d, _ := New(50, 10)
	regOne(t, d, true, "d/a", "X")
	regOne(t, d, true, "d/b", "X")
	regOne(t, d, true, "d/c", "X")
	regOne(t, d, false, "e/a", "X")
	regOne(t, d, false, "e/z", "X")
	want := &Result{
		Renames: []Rename{
			{From: "d/a", To: "e/a", Score: 100},
			{From: "d/b", To: "e/z", Score: 100},
		},
		UnpairedDeleted: []string{"d/c"},
		UnpairedAdded:   []string{},
	}
	if got := d.Detect(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestScoreThresholdBoundary(t *testing.T) {
	d, _ := New(50, 10)
	regOne(t, d, true, "d", "x\nx\n")
	regOne(t, d, false, "ok", "x\n")
	got := d.Detect()
	if len(got.Renames) != 1 || got.Renames[0].Score != 50 {
		t.Fatalf("score==T must be kept, got %+v", got)
	}

	d2, _ := New(50, 10)
	regOne(t, d2, true, "d", "x\nx\nx\n")
	regOne(t, d2, false, "lo", "x\n")
	if got := d2.Detect(); len(got.Renames) != 0 {
		t.Fatalf("score<T must be dropped, got %+v", got)
	}
}

func TestScoreFloor99(t *testing.T) {
	big := strings.Repeat("x", 998) + "\n"
	d, _ := New(99, 10)
	regOne(t, d, true, "d", big+"\n")
	regOne(t, d, false, "a", big)
	got := d.Detect()
	if len(got.Renames) != 1 || got.Renames[0].Score != 99 {
		t.Fatalf("got %+v, want score 99", got)
	}

	d2, _ := New(100, 10)
	regOne(t, d2, true, "d", big+"\n")
	regOne(t, d2, false, "a", big)
	if got := d2.Detect(); len(got.Renames) != 0 {
		t.Fatalf("99 must not match at T=100, got %+v", got)
	}
}

func TestFinalNewlineIsDifferentLine(t *testing.T) {
	d, _ := New(1, 10)
	regOne(t, d, true, "f", "a\n")
	regOne(t, d, false, "g", "a")
	if got := d.Detect(); len(got.Renames) != 0 {
		t.Fatalf("\"a\\n\" and \"a\" share no lines, got %+v", got)
	}

	d2, _ := New(1, 10)
	regOne(t, d2, true, "e", "")
	regOne(t, d2, false, "n", "\n")
	if got := d2.Detect(); len(got.Renames) != 0 {
		t.Fatalf("empty and \"\\n\" must not match, got %+v", got)
	}
}

func TestCompetitionHighScoreWins(t *testing.T) {
	d, _ := New(30, 10)
	regOne(t, d, true, "dir/file", "a\nb\nc\nd\n")
	regOne(t, d, false, "x/near", "a\nb\nc\nd\nextra\n")
	regOne(t, d, false, "x/far", "a\nb\nz\n")
	got := d.Detect()
	if len(got.Renames) != 1 || got.Renames[0].To != "x/near" {
		t.Fatalf("higher-score add must win, got %+v", got)
	}
	if !reflect.DeepEqual(got.UnpairedAdded, []string{"x/far"}) {
		t.Fatalf("unpaired adds = %v", got.UnpairedAdded)
	}
}

func TestTieSameNameWins(t *testing.T) {
	d, _ := New(30, 10)
	regOne(t, d, true, "dir/same.txt", "a\nb\nc\n")
	content := "a\nb\nc\nd\n"
	regOne(t, d, false, "x/other.txt", content)
	regOne(t, d, false, "y/same.txt", content)
	got := d.Detect()
	if len(got.Renames) != 1 || got.Renames[0].To != "y/same.txt" {
		t.Fatalf("same basename must win on tie, got %+v", got)
	}
	if !reflect.DeepEqual(got.UnpairedAdded, []string{"x/other.txt"}) {
		t.Fatalf("unpaired adds = %v", got.UnpairedAdded)
	}
}

type testFile struct {
	path    string
	content string
}

func TestPruningCounter(t *testing.T) {
	d, _ := New(50, 100)
	dels := []testFile{
		{"d/small1", "ab"},
		{"d/small2", "cd"},
		{"d/med", strings.Repeat("m", 50)},
	}
	adds := []testFile{
		{"a/tiny", "ef"},
		{"a/med2", strings.Repeat("n", 50)},
		{"a/huge", strings.Repeat("z", 500)},
	}
	for _, f := range dels {
		regOne(t, d, true, f.path, f.content)
	}
	for _, f := range adds {
		regOne(t, d, false, f.path, f.content)
	}
	d.Detect()
	var want int
	for _, x := range dels {
		for _, y := range adds {
			mn, mx := len(x.content), len(y.content)
			if mn > mx {
				mn, mx = mx, mn
			}
			if mn*100/mx >= 50 {
				want++
			}
		}
	}
	if got := d.commonComputedCount(); got != want {
		t.Fatalf("C computed %d times, want %d", got, want)
	}
	if want == 0 {
		t.Fatal("test setup must leave unpruned combos")
	}
}

func TestMemoizedDetect(t *testing.T) {
	d, _ := New(50, 10)
	regOne(t, d, true, "a", "x\n")
	regOne(t, d, false, "b", "x\n")
	r1 := d.Detect()
	r2 := d.Detect()
	if r1 != r2 {
		t.Fatal("repeated Detect must return the same result")
	}
	if runs := d.detectRunCount(); runs != 1 {
		t.Fatalf("algorithm ran %d times, want 1", runs)
	}
}
