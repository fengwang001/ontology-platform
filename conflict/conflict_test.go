package conflict

import (
	"strings"
	"sync"
	"testing"
)

func mustParse(t *testing.T, text string, L, maxLines int) *Doc {
	t.Helper()
	d, err := Parse(text, L, maxLines)
	if err != nil {
		t.Fatalf("Parse(%q, %d) unexpected error: %v", text, L, err)
	}
	return d
}

func parseErr(t *testing.T, text string, L, maxLines int) *Error {
	t.Helper()
	_, err := Parse(text, L, maxLines)
	if err == nil {
		t.Fatalf("Parse(%q, %d) expected error, got nil", text, L)
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("Parse(%q, %d) error type %T, want *Error", text, L, err)
	}
	return ce
}

func resolveErr(t *testing.T, d *Doc, i int, c Choice, custom []string) *Error {
	t.Helper()
	err := d.Resolve(i, c, custom)
	if err == nil {
		t.Fatalf("Resolve(%d, %d) expected error, got nil", i, c)
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("Resolve(%d, %d) error type %T, want *Error", i, c, err)
	}
	return ce
}

func mustResolve(t *testing.T, d *Doc, i int, c Choice, custom []string) {
	t.Helper()
	if err := d.Resolve(i, c, custom); err != nil {
		t.Fatalf("Resolve(%d, %d) unexpected error: %v", i, c, err)
	}
}

// A body line of exactly seven '=' forces L' = 8 on Render, while the same
// line is a separator marker for Parse(·, 7).
func TestBodyLineOfSevenEqualsForcesL8(t *testing.T) {
	text := "a\n=======\nb\n"
	d := mustParse(t, text, 8, 100)
	if got := d.Render(); got != text {
		t.Fatalf("Render = %q, want %q", got, text)
	}
	// Re-parse with L' = 8: still body, renders identically.
	d2 := mustParse(t, d.Render(), 8, 100)
	if got := d2.Render(); got != text {
		t.Fatalf("roundtrip Render = %q, want %q", got, text)
	}
	// With L = 7 the same line is a marker line (stray separator here).
	if ce := parseErr(t, text, 7, 100); ce.Kind != ErrStray || ce.Line != 2 {
		t.Fatalf("Parse(·,7) = %v, want ErrStray at line 2", ce)
	}
	// A doc with a block plus the body line renders markers with L' = 8.
	doc := "<<<<<<<<\nx\n========\ny\n>>>>>>>>\n=======\n"
	d3 := mustParse(t, doc, 8, 100)
	want := doc
	if got := d3.Render(); got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	d4 := mustParse(t, want, 8, 100)
	if got := d4.Render(); got != want {
		t.Fatalf("roundtrip Render = %q, want %q", got, want)
	}
}

// Lines with L-1 or L+1 marker characters are body, as are lines whose run
// of L is followed by a non-space character.
func TestMarkerRunLengths(t *testing.T) {
	text := "<<<<<<\n<<<<<<<<\n=======x\n||||||\n>>>>>>>>\n"
	d := mustParse(t, text, 7, 100)
	if d.Blocks() != 0 {
		t.Fatalf("Blocks = %d, want 0", d.Blocks())
	}
	if got := d.Render(); got != text {
		t.Fatalf("Render = %q, want %q", got, text)
	}
	// The 8-char runs force L' = 9; roundtrip stays stable.
	d2 := mustParse(t, d.Render(), 9, 100)
	if got := d2.Render(); got != text {
		t.Fatalf("roundtrip Render = %q, want %q", got, text)
	}
	// Inside a block, L-1 and L+1 runs are content lines too.
	doc := "<<<<<<<\n<<<<<<\n<<<<<<<<\n=======\n||||||\n>>>>>>>>\n>>>>>>>\n"
	d3 := mustParse(t, doc, 7, 100)
	if d3.Blocks() != 1 {
		t.Fatalf("Blocks = %d, want 1", d3.Blocks())
	}
	mustResolve(t, d3, 0, Ours, nil)
	if got, want := d3.Render(), "<<<<<<\n<<<<<<<<\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// Trailing spaces of labels are dropped at parse time.
func TestLabelTrailingSpacesDropped(t *testing.T) {
	doc := "<<<<<<< x   \n=======\n>>>>>>> y \n"
	d := mustParse(t, doc, 7, 100)
	want := "<<<<<<< x\n=======\n>>>>>>> y\n"
	if got := d.Render(); got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	// "<<<<<<< " and "<<<<<<<" both have the empty label.
	d2 := mustParse(t, "<<<<<<< \n=======\n>>>>>>>\n", 7, 100)
	if got, want := d2.Render(), "<<<<<<<\n=======\n>>>>>>>\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	// Interior spaces of labels are preserved.
	d3 := mustParse(t, "<<<<<<< a b  c \n=======\n>>>>>>>\n", 7, 100)
	if got, want := d3.Render(), "<<<<<<< a b  c\n=======\n>>>>>>>\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// Resolving a block shifts the indices of all later blocks down by one.
func TestResolveShiftsIndices(t *testing.T) {
	doc := "<<<<<<< one\na\n=======\nb\n>>>>>>>\nmid\n<<<<<<< two\nc\n=======\nd\n>>>>>>>\n"
	d := mustParse(t, doc, 7, 100)
	if d.Blocks() != 2 {
		t.Fatalf("Blocks = %d, want 2", d.Blocks())
	}
	mustResolve(t, d, 0, Ours, nil)
	if d.Blocks() != 1 {
		t.Fatalf("Blocks = %d, want 1", d.Blocks())
	}
	// The remaining block is the former block 1, now at index 0.
	mustResolve(t, d, 0, Theirs, nil)
	if got, want := d.Render(), "a\nmid\nd\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	if ce := resolveErr(t, d, 0, Ours, nil); ce.Kind != ErrNoSuchBlock {
		t.Fatalf("Resolve on empty doc = %v, want ErrNoSuchBlock", ce)
	}
}

// ours=[a,a]/theirs=[a] and ours=[a]/theirs=[a,a] normalize differently.
func TestNormalizeAsymmetric(t *testing.T) {
	d1 := mustParse(t, "<<<<<<< x\na\na\n=======\na\n>>>>>>> y\n", 7, 100)
	d1.Normalize()
	want1 := "a\n<<<<<<< x\na\n=======\n>>>>>>> y\n"
	if got := d1.Render(); got != want1 {
		t.Fatalf("normalize ours=[a,a] theirs=[a]: Render = %q, want %q", got, want1)
	}

	d2 := mustParse(t, "<<<<<<< x\na\n=======\na\na\n>>>>>>> y\n", 7, 100)
	d2.Normalize()
	want2 := "a\n<<<<<<< x\n=======\na\n>>>>>>> y\n"
	if got := d2.Render(); got != want2 {
		t.Fatalf("normalize ours=[a] theirs=[a,a]: Render = %q, want %q", got, want2)
	}
	if d1.Render() == d2.Render() {
		t.Fatal("the two asymmetric normalizations must differ")
	}
}

// Spec example: prefix p moves out, ours=[p] and theirs=[] remain.
func TestNormalizeSpecExample(t *testing.T) {
	doc := "a\n<<<<<<< x\np\np\n=======\np\n>>>>>>> y\nb\n"
	d := mustParse(t, doc, 7, 100)
	d.Normalize()
	want := "a\np\n<<<<<<< x\np\n=======\n>>>>>>> y\nb\n"
	if got := d.Render(); got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	// Normalize is idempotent.
	d.Normalize()
	if got := d.Render(); got != want {
		t.Fatalf("after second Normalize Render = %q, want %q", got, want)
	}
}

// A block whose ours and theirs both normalize to empty disappears.
func TestNormalizeBlockVanishes(t *testing.T) {
	doc := "head\n<<<<<<<\nx\ny\n=======\nx\ny\n>>>>>>>\ntail\n"
	d := mustParse(t, doc, 7, 100)
	d.Normalize()
	if d.Blocks() != 0 {
		t.Fatalf("Blocks = %d, want 0", d.Blocks())
	}
	if got, want := d.Render(), "head\nx\ny\ntail\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// Base is trimmed only when its head/tail matches the moved-out lines.
func TestNormalizeBaseConditionalTrim(t *testing.T) {
	// Prefix and suffix both match base: base is trimmed on both sides.
	doc := "<<<<<<<\np\nx\ns\n|||||||\np\nq\ns\n=======\np\ny\ns\n>>>>>>>\n"
	d := mustParse(t, doc, 7, 100)
	d.Normalize()
	want := "p\n<<<<<<<\nx\n|||||||\nq\n=======\ny\n>>>>>>>\ns\n"
	if got := d.Render(); got != want {
		t.Fatalf("matching base: Render = %q, want %q", got, want)
	}

	// Base head does not match the moved-out prefix: base stays untouched,
	// and the suffix trim still applies to the remaining base.
	doc2 := "<<<<<<<\np\nx\ns\n|||||||\nz\nq\ns\n=======\np\ny\ns\n>>>>>>>\n"
	d2 := mustParse(t, doc2, 7, 100)
	d2.Normalize()
	want2 := "p\n<<<<<<<\nx\n|||||||\nz\nq\n=======\ny\n>>>>>>>\ns\n"
	if got := d2.Render(); got != want2 {
		t.Fatalf("non-matching base head: Render = %q, want %q", got, want2)
	}

	// Base matches neither prefix nor suffix: fully preserved.
	doc3 := "<<<<<<<\np\nx\ns\n|||||||\nz\nq\nw\n=======\np\ny\ns\n>>>>>>>\n"
	d3 := mustParse(t, doc3, 7, 100)
	d3.Normalize()
	want3 := "p\n<<<<<<<\nx\n|||||||\nz\nq\nw\n=======\ny\n>>>>>>>\ns\n"
	if got := d3.Render(); got != want3 {
		t.Fatalf("non-matching base: Render = %q, want %q", got, want3)
	}
}

// After resolving the block containing a 7-'<' line, L' drops back to 7.
func TestMarkerLengthReturnsToSeven(t *testing.T) {
	doc := "<<<<<<<<\n<<<<<<<\n========\nt\n>>>>>>>>\n<<<<<<<<\nu\n========\nv\n>>>>>>>>\n"
	d := mustParse(t, doc, 8, 100)
	if got := d.Render(); got != doc {
		t.Fatalf("Render = %q, want %q", got, doc)
	}
	// Resolve the first block (the one holding the 7-'<' line) with Theirs.
	mustResolve(t, d, 0, Theirs, nil)
	want := "t\n<<<<<<<\nu\n=======\nv\n>>>>>>>\n"
	if got := d.Render(); got != want {
		t.Fatalf("Render = %q, want %q (markers back to 7)", got, want)
	}
}

// All six parse error kinds carry exact 1-based line numbers, and ties are
// broken with ErrNoNewline last.
func TestParseErrors(t *testing.T) {
	// ErrBadL precedes everything, even a missing final newline.
	if ce := parseErr(t, "x", 6, 100); ce.Kind != ErrBadL {
		t.Fatalf("L=6: got %v, want ErrBadL", ce)
	}

	// ErrStray: '|', '=' or '>' marker outside a block.
	for _, tc := range []struct {
		text string
		line int
	}{
		{"a\n>>>>>>>\nb\n", 2},
		{"|||||||\n", 1},
		{"=======\n", 1},
		{"x\ny\n||||||| lab\n", 3},
	} {
		if ce := parseErr(t, tc.text, 7, 100); ce.Kind != ErrStray || ce.Line != tc.line {
			t.Fatalf("Parse(%q): got %v, want ErrStray at line %d", tc.text, ce, tc.line)
		}
	}

	// ErrNested: '<' marker inside a block.
	if ce := parseErr(t, "<<<<<<<\nx\n<<<<<<<\n", 7, 100); ce.Kind != ErrNested || ce.Line != 3 {
		t.Fatalf("nested: got %v, want ErrNested at line 3", ce)
	}

	// ErrOrder: every mis-ordered marker inside a block.
	for _, tc := range []struct {
		name string
		text string
		line int
	}{
		{"end before sep", "<<<<<<<\n>>>>>>>\n", 2},
		{"end in base phase", "<<<<<<<\n|||||||\n>>>>>>>\n", 3},
		{"base repeated", "<<<<<<<\n|||||||\n|||||||\n=======\n>>>>>>>\n", 3},
		{"base after sep", "<<<<<<<\n=======\n|||||||\n>>>>>>>\n", 3},
		{"sep repeated", "<<<<<<<\n=======\n=======\n>>>>>>>\n", 3},
	} {
		if ce := parseErr(t, tc.text, 7, 100); ce.Kind != ErrOrder || ce.Line != tc.line {
			t.Fatalf("%s: got %v, want ErrOrder at line %d", tc.name, ce, tc.line)
		}
	}

	// ErrUnterminated: line number is the block's start line.
	if ce := parseErr(t, "a\n<<<<<<<\nx\n", 7, 100); ce.Kind != ErrUnterminated || ce.Line != 2 {
		t.Fatalf("unterminated: got %v, want ErrUnterminated at line 2", ce)
	}

	// ErrNoNewline: line number is the last line.
	if ce := parseErr(t, "a\nb", 7, 100); ce.Kind != ErrNoNewline || ce.Line != 2 {
		t.Fatalf("no newline: got %v, want ErrNoNewline at line 2", ce)
	}
	if ce := parseErr(t, "x", 7, 100); ce.Kind != ErrNoNewline || ce.Line != 1 {
		t.Fatalf("no newline single: got %v, want ErrNoNewline at line 1", ce)
	}
	// Empty text is legal.
	if d := mustParse(t, "", 7, 100); d.Render() != "" || d.Blocks() != 0 {
		t.Fatal("empty text must parse to an empty doc")
	}

	// Tie-breaking and smallest-line rules.
	// Stray marker on the last line without newline: ErrStray wins the tie.
	if ce := parseErr(t, ">>>>>>>", 7, 100); ce.Kind != ErrStray || ce.Line != 1 {
		t.Fatalf("tie stray/newline: got %v, want ErrStray at line 1", ce)
	}
	// Unterminated block starting on the last line: ErrUnterminated wins.
	if ce := parseErr(t, "a\n<<<<<<<", 7, 100); ce.Kind != ErrUnterminated || ce.Line != 2 {
		t.Fatalf("tie unterminated/newline: got %v, want ErrUnterminated at line 2", ce)
	}
	// An earlier structural error beats a later missing newline.
	if ce := parseErr(t, "a\n<<<<<<<\nx", 7, 100); ce.Kind != ErrUnterminated || ce.Line != 2 {
		t.Fatalf("unterminated before newline: got %v, want ErrUnterminated at line 2", ce)
	}
	// The smallest line number wins among structural errors.
	if ce := parseErr(t, ">>>>>>>\n<<<<<<<\n", 7, 100); ce.Kind != ErrStray || ce.Line != 1 {
		t.Fatalf("smallest line: got %v, want ErrStray at line 1", ce)
	}
}

// Parse reports syntax errors before size errors.
func TestParseTooLarge(t *testing.T) {
	text := "a\nb\nc\nd\n"
	if ce := parseErr(t, text, 7, 3); ce.Kind != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", ce)
	}
	// Syntax first: stray marker plus tiny limit yields ErrStray.
	if ce := parseErr(t, ">>>>>>>\n", 7, 1); ce.Kind != ErrStray {
		t.Fatalf("got %v, want ErrStray (syntax before size)", ce)
	}
	// A block's marker lines count toward the limit.
	doc := "<<<<<<<\nx\n=======\ny\n>>>>>>>\n"
	if ce := parseErr(t, doc, 7, 4); ce.Kind != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", ce)
	}
	mustParse(t, doc, 7, 5)
}

// Resolve rejections report only the first error in the order
// ErrNoSuchBlock > ErrNoBase > ErrBadLine > ErrTooLarge and never mutate.
func TestResolveRejectionOrder(t *testing.T) {
	newDoc := func() *Doc {
		return mustParse(t, "<<<<<<<\no\n=======\nt\n>>>>>>>\n", 7, 8)
	}

	// Out-of-range index beats everything else.
	d := newDoc()
	before := d.Render()
	if ce := resolveErr(t, d, 1, Base, []string{"bad\nline"}); ce.Kind != ErrNoSuchBlock {
		t.Fatalf("got %v, want ErrNoSuchBlock", ce)
	}
	if ce := resolveErr(t, d, -1, Custom, []string{"bad\nline"}); ce.Kind != ErrNoSuchBlock {
		t.Fatalf("got %v, want ErrNoSuchBlock", ce)
	}

	// ErrNoBase beats ErrBadLine (custom is irrelevant for Base).
	if ce := resolveErr(t, d, 0, Base, []string{"bad\nline"}); ce.Kind != ErrNoBase {
		t.Fatalf("got %v, want ErrNoBase", ce)
	}

	// ErrBadLine beats ErrTooLarge.
	if ce := resolveErr(t, d, 0, Custom, []string{"bad\nline", "x", "x", "x", "x", "x", "x", "x", "x"}); ce.Kind != ErrBadLine {
		t.Fatalf("got %v, want ErrBadLine", ce)
	}

	// ErrTooLarge: custom replacement exceeding MaxLines.
	if ce := resolveErr(t, d, 0, Custom, []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}); ce.Kind != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", ce)
	}

	// All rejections left the document untouched.
	if d.Render() != before || d.Blocks() != 1 {
		t.Fatal("rejected Resolve calls must not mutate the document")
	}
}

// Custom resolution that fits replaces the block with the given lines.
func TestResolveCustomAndChoices(t *testing.T) {
	d := mustParse(t, "<<<<<<<\no1\no2\n=======\nt1\n>>>>>>>\n", 7, 100)
	mustResolve(t, d, 0, Custom, []string{"c1", "c2"})
	if got, want := d.Render(), "c1\nc2\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}

	// Both concatenates ours and theirs without deduplication.
	d2 := mustParse(t, "<<<<<<<\nx\nx\n=======\nx\n>>>>>>>\n", 7, 100)
	mustResolve(t, d2, 0, Both, nil)
	if got, want := d2.Render(), "x\nx\nx\n"; got != want {
		t.Fatalf("Both: Render = %q, want %q", got, want)
	}

	// Base with an empty base section resolves to nothing.
	d3 := mustParse(t, "a\n<<<<<<<\no\n|||||||\n=======\nt\n>>>>>>>\nb\n", 7, 100)
	mustResolve(t, d3, 0, Base, nil)
	if got, want := d3.Render(), "a\nb\n"; got != want {
		t.Fatalf("empty Base: Render = %q, want %q", got, want)
	}

	// Base with content.
	d4 := mustParse(t, "<<<<<<<\no\n|||||||\nb1\nb2\n=======\nt\n>>>>>>>\n", 7, 100)
	mustResolve(t, d4, 0, Base, nil)
	if got, want := d4.Render(), "b1\nb2\n"; got != want {
		t.Fatalf("Base: Render = %q, want %q", got, want)
	}
}

// Concurrent calls behave as some serial execution; run with -race.
func TestConcurrentUse(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 64; i++ {
		sb.WriteString("<<<<<<<\nours\n=======\ntheirs\n>>>>>>>\n")
	}
	d := mustParse(t, sb.String(), 7, 1000000)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				var err error
				switch g % 4 {
				case 0:
					err = d.Resolve(0, Ours, nil)
				case 1:
					err = d.Resolve(0, Theirs, nil)
				case 2:
					err = d.Resolve(0, Both, nil)
				case 3:
					err = d.Resolve(0, Custom, []string{"c"})
				}
				if err != nil {
					return
				}
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = d.Render()
				_ = d.Blocks()
				d.Normalize()
			}
		}()
	}
	wg.Wait()
	if d.Blocks() != 0 {
		t.Fatalf("Blocks = %d, want 0 after all resolves", d.Blocks())
	}
	// The result must be parseable and render-stable.
	out := d.Render()
	d2 := mustParse(t, out, 7, 1000000)
	if got := d2.Render(); got != out {
		t.Fatalf("concurrent result not render-stable: %q vs %q", got, out)
	}
}
