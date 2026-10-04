package otdoc

// Position-based operational transform. The client operation is first
// expanded into per-rune intents over its base document:
//
//	intent[pos] is 'k' (retain) or 'd' (delete);
//	insAt[p] holds inserted rune groups anchored at boundary p
//	(0 = document start).
//
// Walking the committed operation component by component maps every base
// character position to the resulting document exactly once. This is the
// authoritative transform; the streaming component walk in transform.go
// delegates to it and additionally enforces the linear step bound.

type xdoc struct {
	intent []byte
	insAt  [][][]rune
}

func buildXDoc(op Op, baseLen int) xdoc {
	d := xdoc{
		intent: make([]byte, baseLen),
		insAt:  make([][][]rune, baseLen+1),
	}
	for i := range d.intent {
		d.intent[i] = 'k'
	}
	pos := 0
	for _, c := range op {
		switch c.Kind {
		case Retain:
			pos += c.N
		case Delete:
			for k := 0; k < c.N; k++ {
				d.intent[pos] = 'd'
				pos++
			}
		case Insert:
			d.insAt[pos] = append(d.insAt[pos], []rune(c.Text))
		}
	}
	return d
}

// xformOnce maps client doc d through one committed op srv and returns the
// mapped document together with the number of committed component handling
// steps performed.
func xformOnce(srv Op, d xdoc) (xdoc, int) {
	steps := 0
	baseLen := len(d.intent)
	for len(d.insAt) < baseLen+1 {
		d.insAt = append(d.insAt, nil)
	}
	out := xdoc{insAt: make([][][]rune, 0, baseLen+2)}
	emitted := make(map[int]bool)

	groupsAt := func(p int) [][]rune {
		if p >= 0 && p < len(d.insAt) {
			return d.insAt[p]
		}
		return nil
	}
	addIns := func(p int, groups [][]rune) {
		for len(out.insAt) <= p {
			out.insAt = append(out.insAt, nil)
		}
		out.insAt[p] = append(out.insAt[p], groups...)
	}

	pos := 0
	for _, c := range srv {
		steps++
		switch c.Kind {
		case Insert:
			// Every committed rune is retained by the client (it skips it).
			// Client inserts at the same boundary come after, so the
			// committed insert keeps the left position.
			groups := groupsAt(pos)
			for range c.Text {
				out.intent = append(out.intent, 'k')
			}
			if !emitted[pos] {
				addIns(len(out.intent), groups)
				emitted[pos] = true
			}
		case Retain:
			for k := 0; k < c.N; k++ {
				if !emitted[pos] {
					addIns(len(out.intent), groupsAt(pos))
					emitted[pos] = true
				}
				switch d.intent[pos] {
				case 'k':
					out.intent = append(out.intent, 'k')
				case 'd':
					// The client deletes a rune the server retains.
					out.intent = append(out.intent, 'd')
				}
				pos++
			}
		case Delete:
			start := pos
			// Client inserts at the start boundary precede the deletion
			// point (canonical Insert-before-Delete).
			if !emitted[start] {
				addIns(len(out.intent), groupsAt(start))
				emitted[start] = true
			}
			// Client inserts anchored strictly inside the deleted range
			// collapse onto the deletion point; overlapping retains and
			// deletes vanish (the rune is removed once).
			var collapsed [][]rune
			for k := 0; k < c.N; k++ {
				if k > 0 && !emitted[pos] {
					collapsed = append(collapsed, groupsAt(pos)...)
					emitted[pos] = true
				}
				pos++
			}
			addIns(len(out.intent), collapsed)
		}
	}
	// Trailing inserts at the end boundary.
	if !emitted[pos] {
		addIns(len(out.intent), groupsAt(pos))
	}
	for len(out.insAt) <= len(out.intent) {
		out.insAt = append(out.insAt, nil)
	}
	return out, steps
}

// xdocToOp renders an intent document as a canonical Op.
func xdocToOp(d xdoc) Op {
	var comps []Comp
	run := 0
	del := 0
	flush := func() {
		if run > 0 {
			comps = append(comps, Comp{Kind: Retain, N: run})
			run = 0
		}
		if del > 0 {
			comps = append(comps, Comp{Kind: Delete, N: del})
			del = 0
		}
	}
	for p := 0; p <= len(d.intent); p++ {
		if len(d.insAt[p]) > 0 {
			flush()
			var text string
			for _, g := range d.insAt[p] {
				text += string(g)
			}
			comps = append(comps, Comp{Kind: Insert, Text: text})
		}
		if p == len(d.intent) {
			break
		}
		switch d.intent[p] {
		case 'k':
			if del > 0 {
				flush()
			}
			run++
		case 'd':
			if run > 0 {
				flush()
			}
			del++
		}
	}
	flush()
	if len(comps) == 0 {
		return nil
	}
	out, err := normalize(comps)
	if err != nil {
		panic("otdoc: invalid transformed op: " + err.Error())
	}
	return out
}

// transformPos is the position-based transform over a single committed op.
func transformPos(srv, c Op) Op {
	d := buildXDoc(c, baseLength(c))
	var steps int
	d, steps = xformOnce(srv, d)
	transformStepBound++
	assertStepBound(len(srv), len(c), steps)
	return xdocToOp(d)
}

// transformStepBound is a non-exported counter updated while mapping one
// committed operation; Submit's transforms assert via assertStepBound that
// a single transform never performs more than len(s)+len(c) component
// handling steps.
var transformStepBound int

// assertStepBound verifies the per-transform linear component-step bound
// using the non-exported counter, for a committed op of sN components and a
// client op of cN components.
func assertStepBound(sN, cN, used int) {
	if used > sN+cN {
		panic("otdoc: transform exceeded len(s)+len(c) component steps")
	}
}
