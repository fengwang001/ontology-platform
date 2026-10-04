package otdoc

// Canonical position-based reference transform. This is an independent
// reimplementation of the same OT rules described in the task, written in
// the "walk every base position one by one" style, used to cross-check the
// component-based transform in transform.go.
//
// The client op is represented as per-rune intents over its base document:
// intent[pos] is 'k' (keep) or 'd' (delete); inserts are lists anchored
// after a base position (anchor 0 = document start).

type refDoc struct {
	// kept/dropped per base position, length == base length.
	intent []byte
	// inserts anchored after position p (p in 0..baseLen), as rune lists.
	insAt [][][]rune
}

func refBuild(op Op, baseLen int) refDoc {
	d := refDoc{intent: make([]byte, baseLen), insAt: make([][][]rune, baseLen+1)}
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

// refXform maps client doc d through committed op srv and rebuilds a client
// intent document against srv's output. srv fully covers its own base.
func refXform(srv Op, d refDoc) refDoc {
	baseLen := len(d.intent)
	for len(d.insAt) < baseLen+1 {
		d.insAt = append(d.insAt, nil)
	}
	out := refDoc{insAt: make([][][]rune, 0, baseLen+2)}
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
	addKeep := func() { out.intent = append(out.intent, 'k') }
	addDrop := func() { out.intent = append(out.intent, 'd') }

	pos := 0
	for _, c := range srv {
		switch c.Kind {
		case Insert:
			rn := []rune(c.Text)
			// Client retains each committed rune. Client inserts at this
			// same boundary come after exactly once (committed wins left).
			groups := groupsAt(pos)
			for range rn {
				addKeep()
			}
			if !emitted[pos] {
				addIns(len(out.intent), groups)
				emitted[pos] = true
			}
		case Retain:
			for k := 0; k < c.N; k++ {
				// Client inserts anchored before this rune (at boundary
				// pos) are emitted at the current output boundary.
				if !emitted[pos] {
					addIns(len(out.intent), groupsAt(pos))
					emitted[pos] = true
				}
				if d.intent[pos] == 'k' {
					addKeep()
				} else if d.intent[pos] == 'd' {
					// Server retains the rune but the client deletes it.
					addDrop()
				}
				pos++
			}
		case Delete:
			// Inserts anchored at the start boundary keep canonical
			// I-before-D ordering and precede the deletion point.
			if !emitted[pos] {
				addIns(len(out.intent), groupsAt(pos))
				emitted[pos] = true
			}
			var collapsed [][]rune
			for k := 0; k < c.N; k++ {
				// Only inserts anchored strictly inside the deleted range
				// (after a deleted rune that is not the last boundary)
				// collapse onto the deletion point. Inserts at the start
				// boundary keep the I-before-D ordering; inserts at the end
				// boundary stay at their own trailing position.
				if k > 0 && !emitted[pos] {
					collapsed = append(collapsed, groupsAt(pos)...)
					emitted[pos] = true
				}
				pos++
			}
			addIns(len(out.intent), collapsed)
		}
	}
	// Trailing inserts anchored at end of document.
	if !emitted[pos] {
		addIns(len(out.intent), groupsAt(pos))
	}
	// Pad insAt to match output rune count plus end boundary.
	for len(out.insAt) <= len(out.intent) {
		out.insAt = append(out.insAt, nil)
	}
	return out
}

// refToOp renders an intent document as a canonical Op.
func (d refDoc) toOp() Op {
	var comps []Comp
	emit := func(kind CompKind, n int) {
		if n == 0 {
			return
		}
		comps = append(comps, Comp{Kind: kind, N: n})
	}
	for p := 0; p <= len(d.intent); p++ {
		// inserts at boundary p first
		if len(d.insAt[p]) > 0 {
			text := ""
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
			emit(Retain, 1)
		case 'd':
			emit(Delete, 1)
		}
	}
	if len(comps) == 0 {
		return nil
	}
	op, err := normalize(comps)
	if err != nil {
		panic("reference: " + err.Error())
	}
	return op
}
