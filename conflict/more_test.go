package conflict

import (
	"strings"
	"sync"
	"testing"
)

// Resolve rejection kinds and their precedence.
func TestResolveRejections(t *testing.T) {
	newDoc := func(t *testing.T) *Doc {
		return mustParse(t, "<<<<<<<\no\n=======\nt\n>>>>>>>\n", 7)
	}

	// ErrNoSuchBlock.
	d := newDoc(t)
	checkResolveErr(t, d, 1, Ours, nil, ErrNoSuchBlock)
	checkResolveErr(t, d, -1, Ours, nil, ErrNoSuchBlock)

	// ErrNoBase: block has no '|' section.
	checkResolveErr(t, d, 0, Base, nil, ErrNoBase)

	// ErrBadLine: custom line contains a newline.
	checkResolveErr(t, d, 0, Custom, []string{"a\nb"}, ErrBadLine)

	// ErrBadChoice.
	checkResolveErr(t, d, 0, Choice(99), nil, ErrBadChoice)

	// Precedence: ErrNoSuchBlock > ErrNoBase > ErrBadLine > ErrTooLarge.
	checkResolveErr(t, d, 7, Base, nil, ErrNoSuchBlock)
	checkResolveErr(t, d, 0, Base, []string{"a\nb"}, ErrNoBase)

	d2, err := Parse("<<<<<<<\no\n=======\nt\n>>>>>>>\n", 7, 5)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkResolveErr(t, d2, 0, Custom, []string{"a\nb", "1", "2", "3", "4", "5", "6"}, ErrBadLine)
}

// Base resolution with an empty base section yields the empty text.
func TestResolveBaseEmpty(t *testing.T) {
	d := mustParse(t, "a\n<<<<<<<\n|||||||\n=======\n>>>>>>>\nb\n", 7)
	mustResolve(t, d, 0, Base, nil)
	if got := d.Render(); got != "a\nb\n" {
		t.Fatalf("Render = %q, want %q", got, "a\nb\n")
	}
}

// Both keeps ours lines followed by theirs lines, without dedup.
func TestResolveBoth(t *testing.T) {
	d := mustParse(t, "<<<<<<<\nx\nx\n=======\nx\n>>>>>>>\n", 7)
	mustResolve(t, d, 0, Both, nil)
	if got := d.Render(); got != "x\nx\nx\n" {
		t.Fatalf("Render = %q, want %q", got, "x\nx\nx\n")
	}
}

// The example from the specification.
func TestSpecExample(t *testing.T) {
	d := mustParse(t, "a\n<<<<<<< x\np\np\n=======\np\n>>>>>>> y\nb\n", 7)
	d.Normalize()
	want := "a\np\n<<<<<<< x\np\n=======\n>>>>>>> y\nb\n"
	if got := d.Render(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Render output re-parsed with the current L' renders identically.
func TestRenderRoundTrip(t *testing.T) {
	texts := []string{
		"",
		"hello\n",
		"<<<<<<< a\nx\n=======\ny\n>>>>>>> b\n",
		"<<<<<<<<<\n<<<<<<<<\n|||||||||\n=========\n>>>>>>>>>\n",
		"<<<<<<<\n<<<<<<\n=======\n>>>>>>>>\n>>>>>>>\n",
		"t1\n<<<<<<<\n=======\n>>>>>>>\nt2\n<<<<<<< l\nq\n||||||| b\nbase\n=======\nz\n>>>>>>> r\n",
	}
	for _, text := range texts {
		d := mustParse(t, text, 7)
		out := d.Render()
		d2, err := Parse(out, d.MarkerLen(), bigMax)
		if err != nil {
			t.Fatalf("reparse of %q: %v", out, err)
		}
		if got := d2.Render(); got != out {
			t.Fatalf("round trip of %q: got %q", text, got)
		}
	}
}

// Normalize is idempotent.
func TestNormalizeIdempotent(t *testing.T) {
	text := "h\n<<<<<<< x\np\na\ns\n|||||||\np\nb\ns\n=======\np\nc\ns\n>>>>>>> y\n" +
		"<<<<<<<\nq\nq\n=======\nq\nq\n>>>>>>>\ntail\n"
	d := mustParse(t, text, 7)
	d.Normalize()
	once := d.Render()
	d.Normalize()
	if got := d.Render(); got != once {
		t.Fatalf("Normalize not idempotent: %q vs %q", once, got)
	}
}

// Replaying the same operation sequence yields identical results.
func TestReplayDeterminism(t *testing.T) {
	text := "<<<<<<< a\nx\n=======\ny\n>>>>>>>\nm\n<<<<<<< b\np\n|||||||\nb\n=======\nq\n>>>>>>>\n"
	ops := func(d *Doc) []string {
		var errs []string
		record := func(err error) {
			if err == nil {
				errs = append(errs, "<nil>")
			} else {
				errs = append(errs, err.Error())
			}
		}
		record(d.Resolve(1, Both, nil))
		d.Normalize()
		record(d.Resolve(0, Custom, []string{"c1", "c2"}))
		record(d.Resolve(3, Ours, nil))
		d.Normalize()
		return errs
	}
	d1 := mustParse(t, text, 7)
	d2 := mustParse(t, text, 7)
	e1, e2 := ops(d1), ops(d2)
	if d1.Render() != d2.Render() {
		t.Fatalf("replay renders differ: %q vs %q", d1.Render(), d2.Render())
	}
	for i := range e1 {
		if e1[i] != e2[i] {
			t.Fatalf("replay error %d differs: %q vs %q", i, e1[i], e2[i])
		}
	}
}

// Concurrent calls behave as some serial order (run with -race).
func TestConcurrent(t *testing.T) {
	var text strings.Builder
	for i := 0; i < 50; i++ {
		text.WriteString("<<<<<<<\na\n=======\nb\n>>>>>>>\n")
	}
	d := mustParse(t, text.String(), 7)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				_ = d.Blocks()
				_ = d.Render()
				_ = d.MarkerLen()
				d.Normalize()
				_ = d.Resolve(0, Choice(g%5), []string{"z"})
			}
		}(g)
	}
	wg.Wait()
	// The document must still be renderable and re-parseable.
	out := d.Render()
	if _, err := Parse(out, d.MarkerLen(), bigMax); err != nil {
		t.Fatalf("post-concurrency reparse: %v", err)
	}
}
