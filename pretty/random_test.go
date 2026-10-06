package pretty

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// This file contains an independent naive implementation of the layout
// rules, written as a direct transcription of the specification: for every
// group it exhaustively measures the flat rendering (including following
// content up to the first broken breakable or forced newline) and picks the
// mode by the rule. The optimized engine in render.go must produce exactly
// the same output on a large number of random documents.

type naiveCmd struct {
	ind  int
	flat bool
	d    Doc
}

// naiveRender renders root following the specification literally. It logs
// every group decision (the "判定依据") to the test log.
func naiveRender(t *testing.T, frags map[string]Doc, root Doc, width int) string {
	t.Helper()
	var out strings.Builder
	col := 0
	newline := func(ind int) {
		out.WriteByte('\n')
		out.WriteString(strings.Repeat(" ", ind))
		col = ind
	}
	var process func(cmds []naiveCmd)
	process = func(cmds []naiveCmd) {
		if len(cmds) == 0 {
			return
		}
		c := cmds[0]
		rest := cmds[1:]
		switch n := c.d.(type) {
		case *textNode:
			out.WriteString(n.s)
			col += Width(n.s)
		case *spaceNode:
			if c.flat {
				out.WriteByte(' ')
				col++
			} else {
				newline(c.ind)
			}
		case *softNode:
			if !c.flat {
				newline(c.ind)
			}
		case *hardNode:
			newline(c.ind)
		case *condNode:
			if c.flat {
				out.WriteString(n.flat)
				col += Width(n.flat)
			} else {
				out.WriteString(n.broken)
				col += Width(n.broken)
			}
		case *indentNode:
			process(append([]naiveCmd{{c.ind + n.n, c.flat, n.body}}, rest...))
			return
		case *alignNode:
			process(append([]naiveCmd{{col, c.flat, n.body}}, rest...))
			return
		case *seqNode:
			cs := make([]naiveCmd, 0, len(n.parts)+len(rest))
			for _, p := range n.parts {
				cs = append(cs, naiveCmd{c.ind, c.flat, p})
			}
			process(append(cs, rest...))
			return
		case *refNode:
			process(append([]naiveCmd{{c.ind, c.flat, frags[n.name]}}, rest...))
			return
		case *groupNode:
			flat := false
			if naiveHasHard(frags, n.body, map[Doc]bool{}) {
				t.Logf("    group @col=%d: contains forced newline -> break", col)
			} else {
				w := naiveMeasure(frags, append([]naiveCmd{{c.ind, true, n.body}}, rest...))
				flat = w <= width-col
				decision := "break"
				if flat {
					decision = "flat"
				}
				t.Logf("    group @col=%d: flat width until first break = %d, remaining = %d -> %s",
					col, w, width-col, decision)
			}
			process(append([]naiveCmd{{c.ind, flat, n.body}}, rest...))
			return
		}
		process(rest)
	}
	process([]naiveCmd{{0, false, root}})
	lines := strings.Split(out.String(), "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " ")
	}
	return strings.Join(lines, "\n")
}

// naiveHasHard reports whether the subtree contains a forced newline.
func naiveHasHard(frags map[string]Doc, d Doc, memo map[Doc]bool) bool {
	if b, ok := memo[d]; ok {
		return b
	}
	r := false
	switch n := d.(type) {
	case *hardNode:
		r = true
	case *indentNode:
		r = naiveHasHard(frags, n.body, memo)
	case *alignNode:
		r = naiveHasHard(frags, n.body, memo)
	case *groupNode:
		r = naiveHasHard(frags, n.body, memo)
	case *seqNode:
		for _, p := range n.parts {
			if naiveHasHard(frags, p, memo) {
				r = true
				break
			}
		}
	case *refNode:
		r = naiveHasHard(frags, frags[n.name], memo)
	}
	memo[d] = r
	return r
}

// naiveMeasure renders the commands flat (undecided groups flat) into an
// actual string, stopping at the first breakable in break mode or forced
// newline, and returns the width of the produced text.
func naiveMeasure(frags map[string]Doc, cmds []naiveCmd) int {
	var sb strings.Builder
	var walk func(cmds []naiveCmd) (stopped bool)
	walk = func(cmds []naiveCmd) bool {
		if len(cmds) == 0 {
			return false
		}
		c := cmds[0]
		rest := cmds[1:]
		switch n := c.d.(type) {
		case *textNode:
			sb.WriteString(n.s)
		case *spaceNode:
			if !c.flat {
				return true
			}
			sb.WriteByte(' ')
		case *softNode:
			if !c.flat {
				return true
			}
		case *hardNode:
			return true
		case *condNode:
			if c.flat {
				sb.WriteString(n.flat)
			} else {
				sb.WriteString(n.broken)
			}
		case *indentNode:
			return walk(append([]naiveCmd{{c.ind, c.flat, n.body}}, rest...))
		case *alignNode:
			return walk(append([]naiveCmd{{c.ind, c.flat, n.body}}, rest...))
		case *seqNode:
			cs := make([]naiveCmd, 0, len(n.parts)+len(rest))
			for _, p := range n.parts {
				cs = append(cs, naiveCmd{c.ind, c.flat, p})
			}
			return walk(append(cs, rest...))
		case *refNode:
			return walk(append([]naiveCmd{{c.ind, c.flat, frags[n.name]}}, rest...))
		case *groupNode:
			// Not yet decided: counts as flat.
			return walk(append([]naiveCmd{{c.ind, true, n.body}}, rest...))
		}
		return walk(rest)
	}
	walk(cmds)
	return Width(sb.String())
}

// overwideOf recomputes the overwide-line list from a trimmed output.
func overwideOf(output string, width int) []LineInfo {
	var over []LineInfo
	for i, ln := range strings.Split(output, "\n") {
		if w := Width(ln); w > width {
			over = append(over, LineInfo{Line: i + 1, Width: w})
		}
	}
	return over
}

var randomTexts = []string{
	"a", "bb", "ccc", "ddddd", "x y", "z ", " q", "", "longertext",
	"你好", "界", "ab界", "表 格", "·",
}

func genDoc(r *rand.Rand, frags []string, depth int) Doc {
	leaf := func() Doc {
		switch r.Intn(4) {
		case 0:
			return BreakableSpace()
		case 1:
			return BreakableEmpty()
		case 2:
			return Cond(randomTexts[r.Intn(len(randomTexts))], randomTexts[r.Intn(len(randomTexts))])
		default:
			return Text(randomTexts[r.Intn(len(randomTexts))])
		}
	}
	if depth <= 0 {
		return leaf()
	}
	switch r.Intn(14) {
	case 0, 1, 2, 3:
		return leaf()
	case 4:
		return HardLine()
	case 5:
		return Indent(r.Intn(4), genDoc(r, frags, depth-1))
	case 6:
		return Align(genDoc(r, frags, depth-1))
	case 7, 8:
		return Group(genDoc(r, frags, depth-1))
	case 9:
		if len(frags) > 0 {
			return Ref(frags[r.Intn(len(frags))])
		}
		return leaf()
	default:
		n := r.Intn(5)
		parts := make([]Doc, 0, n)
		for i := 0; i < n; i++ {
			parts = append(parts, genDoc(r, frags, depth-1))
		}
		return Seq(parts...)
	}
}

func TestRandomDocsMatchNaiveModel(t *testing.T) {
	const cases = 300
	for i := 0; i < cases; i++ {
		seed := int64(i)*7919 + 13
		r := rand.New(rand.NewSource(seed))

		s := NewSession()
		var fragNames []string
		fragDump := strings.Builder{}
		snap := map[string]Doc{}
		for k := 0; k < r.Intn(4); k++ {
			name := fmt.Sprintf("frag%d", k)
			fd := genDoc(r, fragNames, 3)
			if err := s.Register(name, fd); err != nil {
				t.Fatalf("case %d: register %s: %v", i, name, err)
			}
			fragNames = append(fragNames, name)
			snap[name] = fd
			fmt.Fprintf(&fragDump, " %s=%s", name, dumpDoc(fd))
		}

		doc := genDoc(r, fragNames, 5)
		width := 1 + r.Intn(25)
		dump := dumpDoc(doc)

		t.Logf("case %d seed=%d width=%d\n  doc: %s\n  fragments:%s", i, seed, width, dump, fragDump.String())

		out, over, err := s.Render(doc, width)
		if err != nil {
			t.Fatalf("case %d: engine error: %v", i, err)
		}
		want := naiveRender(t, snap, doc, width)
		t.Logf("  output: %q", out)
		if out != want {
			t.Fatalf("case %d seed=%d width=%d\ndoc: %s\nengine: %q\nnaive:  %q",
				i, seed, width, dump, out, want)
		}
		if wantOver := overwideOf(want, width); !reflect.DeepEqual(over, wantOver) {
			t.Fatalf("case %d: overwide = %+v, want %+v", i, over, wantOver)
		}
		// Rendering must not mutate the tree.
		if after := dumpDoc(doc); after != dump {
			t.Fatalf("case %d: doc mutated by render", i)
		}
		// Determinism: rendering again gives the same result.
		out2, _, err := s.Render(doc, width)
		if err != nil || out2 != out {
			t.Fatalf("case %d: non-deterministic render", i)
		}
	}
}

// FuzzRenderMatchesNaive drives the same comparison from arbitrary bytes.
func FuzzRenderMatchesNaive(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{255, 0, 128, 64, 32, 16})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			t.Skip()
		}
		br := &byteRand{data: data}
		s := NewSession()
		snap := map[string]Doc{}
		var fragNames []string
		for k := 0; k < br.intn(3); k++ {
			name := fmt.Sprintf("frag%d", k)
			fd := genDoc(&br.r, fragNames, 3)
			if err := s.Register(name, fd); err != nil {
				t.Fatalf("register: %v", err)
			}
			fragNames = append(fragNames, name)
			snap[name] = fd
		}
		doc := genDoc(&br.r, fragNames, 5)
		width := 1 + br.intn(25)
		out, over, err := s.Render(doc, width)
		if err != nil {
			t.Fatalf("engine error: %v (doc %s)", err, dumpDoc(doc))
		}
		want := naiveRender(t, snap, doc, width)
		if out != want {
			t.Fatalf("width=%d doc: %s\nengine: %q\nnaive:  %q", width, dumpDoc(doc), out, want)
		}
		if wantOver := overwideOf(want, width); !reflect.DeepEqual(over, wantOver) {
			t.Fatalf("overwide = %+v, want %+v", over, wantOver)
		}
	})
}

// byteRand derives a deterministic *rand.Rand plus consumes bytes for
// small choices, so fuzz inputs map onto varied documents.
type byteRand struct {
	data []byte
	pos  int
	r    rand.Rand
}

func (b *byteRand) intn(n int) int {
	if b.pos >= len(b.data) {
		return 0
	}
	v := int(b.data[b.pos]) % n
	b.pos++
	b.r = *rand.New(rand.NewSource(int64(v)*2654435761 + int64(b.pos)))
	return v
}
