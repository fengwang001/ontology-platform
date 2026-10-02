package hygiene

import "testing"

func TestDepthBoundary(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "r", nil, mustParse(t, "(r)"))

	if _, err := session.Expand(mustParse(t, "(r)")); err == nil || err.(ExpandError).Kind != DepthExceeded {
		t.Fatalf("recursive macro err = %v, want DepthExceeded", err)
	}
	if session.BindingCount() != 0 || session.ExpansionCount() != 0 {
		t.Fatalf("rejected expansion changed state: N:%d X:%d", session.BindingCount(), session.ExpansionCount())
	}

	session2 := NewSession()
	mustDef(t, session2, "wrap", []string{"x"}, mustParse(t, "(w x)"))
	mustDef(t, session2, "w", []string{"x"}, mustParse(t, "(z x)"))
	mustDef(t, session2, "z", []string{"x"}, mustParse(t, "(y x)"))
	mustDef(t, session2, "y", []string{"x"}, mustParse(t, "(v x)"))
	mustDef(t, session2, "v", []string{"x"}, mustParse(t, "(u x)"))
	mustDef(t, session2, "u", []string{"x"}, mustParse(t, "(t x)"))
	mustDef(t, session2, "t", []string{"x"}, mustParse(t, "(s x)"))
	mustDef(t, session2, "s", []string{"x"}, mustParse(t, "(q x)"))
	mustDef(t, session2, "q", []string{"x"}, mustParse(t, "(p x)"))
	mustDef(t, session2, "p", []string{"x"}, mustParse(t, "(o x)"))
	mustDef(t, session2, "o", []string{"x"}, mustParse(t, "(n x)"))
	mustDef(t, session2, "n", []string{"x"}, mustParse(t, "(m x)"))
	mustDef(t, session2, "m", []string{"x"}, mustParse(t, "(l x)"))
	mustDef(t, session2, "l", []string{"x"}, mustParse(t, "(k x)"))
	mustDef(t, session2, "k", []string{"x"}, mustParse(t, "(j x)"))
	mustDef(t, session2, "j", []string{"x"}, mustParse(t, "(i x)"))
	mustDef(t, session2, "i", []string{"x"}, mustParse(t, "(h x)"))
	mustDef(t, session2, "h", []string{"x"}, mustParse(t, "(g x)"))
	mustDef(t, session2, "g", []string{"x"}, mustParse(t, "(f x)"))
	mustDef(t, session2, "f", []string{"x"}, mustParse(t, "x"))

	result, err := session2.Expand(mustParse(t, "(wrap 1)"))
	if err != nil {
		t.Fatalf("20 expansions should be allowed: %v", err)
	}
	if got := mustRender(t, result); got != "1" {
		t.Fatalf("got %q, want 1", got)
	}

	mustDef(t, session2, "xwrap", []string{"x"}, mustParse(t, "(wrap x)"))
	_, err = session2.Expand(mustParse(t, "(xwrap 1)"))
	if err == nil || err.(ExpandError).Kind != DepthExceeded {
		t.Fatalf("21st expansion err = %v, want DepthExceeded", err)
	}
}

func TestSizeBoundary(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "big", nil, mustParse(t, "(a (b c d e f g h))"))

	input := sizedMacroTree(t, 175, 47, 2)
	result, err := session.Expand(input)
	if err != nil {
		t.Fatalf("2000 output nodes should be allowed: %#v, input nodes=%d", err, countTermNodes(input))
	}
	if got := countTermNodes(result); got != 2000 {
		t.Fatalf("output nodes = %d, want 2000", got)
	}

	session2 := NewSession()
	mustDef(t, session2, "big", nil, mustParse(t, "(a (b c d e f g h))"))
	_, err = session2.Expand(sizedMacroTree(t, 175, 47, 3))
	if err == nil || err.(ExpandError).Kind != SizeExceeded {
		t.Fatalf("2001+ output nodes err = %v, want SizeExceeded", err)
	}
	if session2.BindingCount() != 0 || session2.ExpansionCount() != 0 {
		t.Fatalf("rejected expansion changed state: N:%d X:%d", session2.BindingCount(), session2.ExpansionCount())
	}
}

func sizedMacroTree(t *testing.T, leaves int, wrappers int, extraAtoms int) *Term {
	t.Helper()
	level1 := make([]*Term, 0, 7)
	level2Counts := []int{5, 5, 5, 5, 5, 4, 4}
	level2Index := 0
	for _, level2Count := range level2Counts {
		children := []*Term{mustParse(t, "join")}
		for index := 0; index < level2Count; index++ {
			groupItems := []*Term{mustParse(t, "join")}
			for slot := 0; slot < 7; slot++ {
				if level2Index*7+slot < leaves {
					groupItems = append(groupItems, mustParse(t, "(big)"))
				} else {
					groupItems = append(groupItems, mustParse(t, "unused"))
				}
			}
			children = append(children, &Term{Kind: List, Items: groupItems})
			level2Index++
		}
		for range 7 - level2Count {
			children = append(children, mustParse(t, "unused"))
		}
		level1 = append(level1, &Term{Kind: List, Items: children})
	}

	root := &Term{Kind: List, Items: append([]*Term{mustParse(t, "join")}, level1...)}
	for index := range wrappers {
		items := []*Term{mustParse(t, "join"), root}
		if index < extraAtoms {
			items = append(items, mustParse(t, "pad"))
		}
		root = &Term{Kind: List, Items: items}
	}
	return root
}
