package conflict

import (
	"errors"
	"testing"
)

const bigMax = 1000000

func mustParse(t *testing.T, text string, L int) *Doc {
	t.Helper()
	d, err := Parse(text, L, bigMax)
	if err != nil {
		t.Fatalf("Parse(%q, %d) unexpected error: %v", text, L, err)
	}
	return d
}

func checkParseErr(t *testing.T, text string, L int, want error, wantLine int) {
	t.Helper()
	_, err := Parse(text, L, bigMax)
	if err == nil {
		t.Fatalf("Parse(%q, %d): want %v, got nil", text, L, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Parse(%q, %d): want %v, got %v", text, L, want, err)
	}
	if want == ErrBadL || want == ErrTooLarge {
		return
	}
	var le *LineError
	if !errors.As(err, &le) {
		t.Fatalf("Parse(%q, %d): error %v is not a LineError", text, L, err)
	}
	if le.Line != wantLine {
		t.Fatalf("Parse(%q, %d): want line %d, got %d", text, L, wantLine, le.Line)
	}
}

func mustResolve(t *testing.T, d *Doc, i int, choice Choice, custom []string) {
	t.Helper()
	if err := d.Resolve(i, choice, custom); err != nil {
		t.Fatalf("Resolve(%d, %d) unexpected error: %v", i, choice, err)
	}
}

func checkResolveErr(t *testing.T, d *Doc, i int, choice Choice, custom []string, want error) {
	t.Helper()
	before := d.Render()
	err := d.Resolve(i, choice, custom)
	if !errors.Is(err, want) {
		t.Fatalf("Resolve(%d, %d): want %v, got %v", i, choice, want, err)
	}
	if got := d.Render(); got != before {
		t.Fatalf("rejected Resolve mutated the document: %q -> %q", before, got)
	}
}

// A body line that is exactly "=======" forces L'=8, and parsing the
// rendered text with L=7 treats it as a separator (stray) line.
func TestBodyLineOfSevenEqualsForcesL8(t *testing.T) {
	d := mustParse(t, "=======\n", 8)
	if got := d.MarkerLen(); got != 8 {
		t.Fatalf("MarkerLen = %d, want 8", got)
	}
	out := d.Render()
	if out != "=======\n" {
		t.Fatalf("Render = %q, want %q", out, "=======\n")
	}
	checkParseErr(t, out, 7, ErrStray, 1)
}

// Lines with L-1 or L+1 marker characters are body lines.
func TestMarkerRunOffByOneIsBody(t *testing.T) {
	text := "<<<<<<\n" + // L-1 = 6 '<'
		"<<<<<<<<\n" + // L+1 = 8 '<'
		"||||||\n" + // 6 '|'
		"=========\n" // 9 '='
	d := mustParse(t, text, 7)
	if d.Blocks() != 0 {
		t.Fatalf("Blocks = %d, want 0", d.Blocks())
	}
	if got := d.Render(); got != text {
		t.Fatalf("Render = %q, want %q", got, text)
	}
	if got := d.MarkerLen(); got != 10 { // max run 9 -> L' = 10
		t.Fatalf("MarkerLen = %d, want 10", got)
	}
}

// Trailing spaces of labels are dropped.
func TestLabelTrailingSpacesDropped(t *testing.T) {
	d := mustParse(t, "<<<<<<< x   \n=======  base label  \n>>>>>>> y \n", 7)
	want := "<<<<<<< x\n=======  base label\n>>>>>>> y\n"
	if got := d.Render(); got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	d2 := mustParse(t, "<<<<<<< \n=======\n>>>>>>>\n", 7)
	if got := d2.Render(); got != "<<<<<<<\n=======\n>>>>>>>\n" {
		t.Fatalf("Render = %q, want bare markers", got)
	}
}

// After resolving a block, the indices of later blocks shift down by 1.
func TestResolveIndexShift(t *testing.T) {
	text := "<<<<<<< a\nA1\n=======\nA2\n>>>>>>>\n" +
		"mid\n" +
		"<<<<<<< b\nB1\n=======\nB2\n>>>>>>>\n"
	d := mustParse(t, text, 7)
	if d.Blocks() != 2 {
		t.Fatalf("Blocks = %d, want 2", d.Blocks())
	}
	mustResolve(t, d, 0, Ours, nil)
	if d.Blocks() != 1 {
		t.Fatalf("Blocks = %d, want 1", d.Blocks())
	}
	// The old block #1 is now block #0.
	mustResolve(t, d, 0, Theirs, nil)
	if got := d.Render(); got != "A1\nmid\nB2\n" {
		t.Fatalf("Render = %q, want %q", got, "A1\nmid\nB2\n")
	}
	if d.Blocks() != 0 {
		t.Fatalf("Blocks = %d, want 0", d.Blocks())
	}
}

// ours=[a,a], theirs=[a] and ours=[a], theirs=[a,a] normalize to
// different results.
func TestNormalizeAsymmetric(t *testing.T) {
	d1 := mustParse(t, "<<<<<<<\na\na\n=======\na\n>>>>>>>\n", 7)
	d1.Normalize()
	want1 := "a\n<<<<<<<\na\n=======\n>>>>>>>\n"
	if got := d1.Render(); got != want1 {
		t.Fatalf("ours=[a,a] theirs=[a]: got %q, want %q", got, want1)
	}

	d2 := mustParse(t, "<<<<<<<\na\n=======\na\na\n>>>>>>>\n", 7)
	d2.Normalize()
	want2 := "a\n<<<<<<<\n=======\na\n>>>>>>>\n"
	if got := d2.Render(); got != want2 {
		t.Fatalf("ours=[a] theirs=[a,a]: got %q, want %q", got, want2)
	}
	if d1.Render() == d2.Render() {
		t.Fatal("the two asymmetric normalizations must differ")
	}
}

// Base lines are dropped only when they match the moved-out prefix or
// suffix exactly.
func TestNormalizeBaseConditionalTrim(t *testing.T) {
	// ours=[p,x,s], base=[p,b,s], theirs=[p,y,s]:
	// prefix [p] and suffix [s] move out; base matches both -> [b].
	d := mustParse(t,
		"<<<<<<<\np\nx\ns\n|||||||\np\nb\ns\n=======\np\ny\ns\n>>>>>>>\n", 7)
	d.Normalize()
	want := "p\n" +
		"<<<<<<<\nx\n|||||||\nb\n=======\ny\n>>>>>>>\n" +
		"s\n"
	if got := d.Render(); got != want {
		t.Fatalf("base trim both ends: got %q, want %q", got, want)
	}

	// Base head does not match the prefix: prefix part kept, suffix
	// still trimmed independently.
	d2 := mustParse(t,
		"<<<<<<<\np\nx\ns\n|||||||\nq\nb\ns\n=======\np\ny\ns\n>>>>>>>\n", 7)
	d2.Normalize()
	want2 := "p\n" +
		"<<<<<<<\nx\n|||||||\nq\nb\n=======\ny\n>>>>>>>\n" +
		"s\n"
	if got := d2.Render(); got != want2 {
		t.Fatalf("base head mismatch: got %q, want %q", got, want2)
	}

	// Base tail does not match the suffix: suffix part kept.
	d3 := mustParse(t,
		"<<<<<<<\np\nx\ns\n|||||||\np\nb\n=======\np\ny\ns\n>>>>>>>\n", 7)
	d3.Normalize()
	want3 := "p\n" +
		"<<<<<<<\nx\n|||||||\nb\n=======\ny\n>>>>>>>\n" +
		"s\n"
	if got := d3.Render(); got != want3 {
		t.Fatalf("base tail mismatch: got %q, want %q", got, want3)
	}

	// Base matches neither: kept whole.
	d4 := mustParse(t,
		"<<<<<<<\np\nx\ns\n|||||||\nq\nb\n=======\np\ny\ns\n>>>>>>>\n", 7)
	d4.Normalize()
	want4 := "p\n" +
		"<<<<<<<\nx\n|||||||\nq\nb\n=======\ny\n>>>>>>>\n" +
		"s\n"
	if got := d4.Render(); got != want4 {
		t.Fatalf("base kept whole: got %q, want %q", got, want4)
	}
}

// After resolving a block whose content held a 7-char marker run, the
// marker length drops back to 7.
func TestMarkerLenDropsAfterResolve(t *testing.T) {
	text := "<<<<<<<<\n" + // open (L=8)
		"<<<<<<<\n" + // ours: a 7-char run, body at L=8
		"========\n" +
		"ok\n" +
		">>>>>>>>\n"
	d := mustParse(t, text, 8)
	if got := d.MarkerLen(); got != 8 {
		t.Fatalf("MarkerLen = %d, want 8", got)
	}
	mustResolve(t, d, 0, Theirs, nil)
	if got := d.MarkerLen(); got != 7 {
		t.Fatalf("MarkerLen after resolve = %d, want 7", got)
	}
	if got := d.Render(); got != "ok\n" {
		t.Fatalf("Render = %q, want %q", got, "ok\n")
	}
}

// The six parse error kinds, their line numbers and the tie-breaking.
func TestParseErrors(t *testing.T) {
	// ErrBadL precedes everything, even syntax errors.
	checkParseErr(t, ">>>>>>>\n", 6, ErrBadL, 0)
	checkParseErr(t, "", 3, ErrBadL, 0)

	// ErrStray: '|', '=' or '>' marker lines outside a block.
	checkParseErr(t, ">>>>>>>\n", 7, ErrStray, 1)
	checkParseErr(t, "a\n=======\n", 7, ErrStray, 2)
	checkParseErr(t, "x\ny\n|||||||\n", 7, ErrStray, 3)
	// Smallest line wins among several strays.
	checkParseErr(t, "=======\na\n>>>>>>>\n", 7, ErrStray, 1)

	// ErrNested: a '<' marker line inside a block (closed later so the
	// block is not unterminated).
	checkParseErr(t, "<<<<<<<\nx\n<<<<<<<\n=======\n>>>>>>>\n", 7, ErrNested, 3)

	// ErrOrder: '>' before '='.
	checkParseErr(t, "<<<<<<<\n>>>>>>>\n=======\n>>>>>>>\n", 7, ErrOrder, 2)
	// ErrOrder: '|' after '='.
	checkParseErr(t, "<<<<<<<\n=======\n|||||||\n>>>>>>>\n", 7, ErrOrder, 3)
	// ErrOrder: '=' repeated.
	checkParseErr(t, "<<<<<<<\n=======\n=======\n>>>>>>>\n", 7, ErrOrder, 3)
	// ErrOrder: '|' repeated.
	checkParseErr(t, "<<<<<<<\n|||||||\n|||||||\n=======\n>>>>>>>\n", 7, ErrOrder, 3)

	// ErrUnterminated: line number is the block's start line.
	checkParseErr(t, "<<<<<<<\nx\n", 7, ErrUnterminated, 1)
	checkParseErr(t, "a\nb\n<<<<<<<\n", 7, ErrUnterminated, 3)
	checkParseErr(t, "<<<<<<<\n|||||||\n", 7, ErrUnterminated, 1)

	// ErrNoNewline: line number is the last line.
	checkParseErr(t, "abc", 7, ErrNoNewline, 1)
	checkParseErr(t, "a\nb", 7, ErrNoNewline, 2)
}

// Separate because the last case above must not error.
func TestParseNoNewlineOK(t *testing.T) {
	mustParse(t, "a\nb\n", 7)
	mustParse(t, "", 7)
}

// Tie-breaking between simultaneously true conditions.
func TestParseErrorTies(t *testing.T) {
	// Stray marker on the last line without a trailing newline: same
	// line, ErrNoNewline loses.
	checkParseErr(t, ">>>>>>>", 7, ErrStray, 1)
	// Unterminated block (start line 1) beats ErrNoNewline (line 2).
	checkParseErr(t, "<<<<<<<\nx", 7, ErrUnterminated, 1)
	// Unterminated block starting on the last line ties with
	// ErrNoNewline; ErrNoNewline is last.
	checkParseErr(t, "<<<<<<<", 7, ErrUnterminated, 1)
	// A stray before an unterminated block keeps the smaller line.
	checkParseErr(t, "=======\n<<<<<<<\n", 7, ErrStray, 1)
	// A nested '<' inside a block that never closes: the
	// unterminated condition (start line 1) has the smaller line.
	checkParseErr(t, "<<<<<<<\n<<<<<<<\n", 7, ErrUnterminated, 1)
}

// Custom resolution beyond MaxLines is rejected with ErrTooLarge.
func TestCustomTooLarge(t *testing.T) {
	d, err := Parse("<<<<<<<\na\n=======\nb\n>>>>>>>\n", 7, 5)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Replacing the 5-line block with 6 lines exceeds MaxLines=5.
	checkResolveErr(t, d, 0, Custom, []string{"1", "2", "3", "4", "5", "6"}, ErrTooLarge)
	// Within the limit it succeeds.
	mustResolve(t, d, 0, Custom, []string{"1", "2"})
	if got := d.Render(); got != "1\n2\n" {
		t.Fatalf("Render = %q, want %q", got, "1\n2\n")
	}
}

// Parse enforces MaxLines after the syntax check.
func TestParseTooLarge(t *testing.T) {
	if _, err := Parse("a\nb\nc\n", 7, 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	// Exactly at the limit is fine.
	if _, err := Parse("a\nb\n", 7, 2); err != nil {
		t.Fatalf("at-limit parse: %v", err)
	}
	// Syntax first: a stray marker beats the size limit.
	if _, err := Parse(">>>>>>>\nx\n", 7, 1); !errors.Is(err, ErrStray) {
		t.Fatalf("syntax must precede ErrTooLarge, got %v", err)
	}
}
