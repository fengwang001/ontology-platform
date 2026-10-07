package docsync

import (
	"fmt"
	"io"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel is an independent reference implementation that rebuilds the whole
// text and maps diagnostic ranges through plain UTF-16 offsets.
type naiveDiag struct {
	id      int
	a, b    int
	sev     Severity
	msg     string
	dead    bool
	deadVer int64
}

type naiveModel struct {
	text string // stored in UTF-16 units as []uint16
	u16  []uint16
	ver  int64
	d    []*naiveDiag
	next int
}

func newNaive(text string) *naiveModel {
	m := &naiveModel{text: text, u16: encode16(text), next: 1}
	return m
}

func encode16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, uint16(hi), uint16(lo))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func (m *naiveModel) posOf(off int) Position {
	line, col := 0, 0
	for i := 0; i < off; i++ {
		if m.u16[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return Position{line, col}
}

func (m *naiveModel) offOf(p Position) (off int, split, ok bool) {
	line, col := 0, 0
	for i := 0; i < len(m.u16); {
		if line == p.Line && col == p.Character {
			return i, false, true
		}
		// surrogate pair middle
		if line == p.Line && col+1 == p.Character &&
			i+1 < len(m.u16) && isHigh(m.u16[i]) && isLow(m.u16[i+1]) {
			return 0, true, false
		}
		if m.u16[i] == '\n' {
			line++
			col = 0
			i++
		} else if isHigh(m.u16[i]) {
			col += 2
			i += 2
		} else {
			col++
			i++
		}
	}
	if line == p.Line && col == p.Character {
		return len(m.u16), false, true
	}
	return 0, false, false
}

func isHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }
func isLow(u uint16) bool  { return 0xDC00 <= u && u <= 0xDFFF }

// safeOffsets returns all UTF-16 offsets that address a legal position: they
// do not split a surrogate pair and do not point at a line-feed unit (feeds are
// not addressable; the line-end column maps to the offset before the feed).
func safeOffsets(u []uint16) []int {
	bad := map[int]bool{}
	for i := 0; i < len(u); i++ {
		if u[i] == '\n' {
			bad[i] = true
		}
		if i+1 < len(u) && isHigh(u[i]) && isLow(u[i+1]) {
			bad[i+1] = true
		}
	}
	var out []int
	for i := 0; i <= len(u); i++ {
		if !bad[i] {
			out = append(out, i)
		}
	}
	return out
}

type naiveEdit struct {
	s, e int
	text []uint16
}

type shift struct{ s, e, nl int }

func (m *naiveModel) apply(base int64, edits []naiveEdit) (int64, bool) {
	if base != m.ver {
		return m.ver, false
	}
	// Build new text via simultaneous coordinates (non-overlap assumed; the
	// test only calls with valid batches).
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].s < edits[j].s })
	var out []uint16
	cursor := 0
	var shifts []shift
	for _, ed := range edits {
		if ed.s < cursor || ed.e < ed.s || ed.e > len(m.u16) {
			panic(fmt.Sprintf("bad edit cursor=%d ed=%+v len=%d all=%v", cursor, ed, len(m.u16), edits))
		}
		out = append(out, m.u16[cursor:ed.s]...)
		out = append(out, ed.text...)
		cursor = ed.e
		shifts = append(shifts, shift{ed.s, ed.e, len(ed.text)})
	}
	out = append(out, m.u16[cursor:]...)

	// Map each diagnostic endpoint independently per protocol.

	newD := make([]*naiveDiag, 0, len(m.d))
	for _, d := range m.d {
		if d.dead {
			newD = append(newD, d)
			continue
		}
		a, b := d.a, d.b
		overlap := false
		for _, ed := range shifts {
			if ed.s == ed.e {
				continue
			}
			positive := b > a && a < ed.e && ed.s < b
			emptyInside := a == b && ed.s < a && a < ed.e
			if positive || emptyInside {
				overlap = true
				break
			}
		}
		if overlap {
			d.dead = true
			d.deadVer = m.ver + 1
			newD = append(newD, d)
			continue
		}
		// End mapping: an empty diagnostic (a==b) moves both endpoints like a
		// start; otherwise the end at an insertion point stays.
		na := mapStartOrig(a, shifts)
		nb := mapEndOrig(b, a == b, shifts)
		d.a, d.b = na, nb
		newD = append(newD, d)
	}
	m.d = newD
	m.u16 = out
	m.text = decode16(out)
	m.ver++
	return m.ver, true
}

func mapStartOrig(orig int, shifts []shift) int {
	total := 0
	for _, ed := range shifts {
		if ed.s == ed.e {
			if orig >= ed.s {
				total += ed.nl
			}
			continue
		}
		if orig > ed.s {
			total += ed.nl - (ed.e - ed.s)
		}
	}
	return orig + total
}

func mapEndOrig(orig int, empty bool, shifts []shift) int {
	total := 0
	for _, ed := range shifts {
		if ed.s == ed.e {
			if orig < ed.s {
				continue
			}
			if orig == ed.s && !empty {
				continue
			}
			total += ed.nl
			continue
		}
		if orig >= ed.e {
			total += ed.nl - (ed.e - ed.s)
		}
	}
	return orig + total
}

func decode16(u []uint16) string {
	runes := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		if isHigh(u[i]) && i+1 < len(u) && isLow(u[i+1]) {
			rn := 0x10000 + (rune(u[i]-0xD800) << 10) + rune(u[i+1]-0xDC00)
			runes = append(runes, rn)
			i++
		} else {
			runes = append(runes, rune(u[i]))
		}
	}
	return string(runes)
}

func (m *naiveModel) register(a, b int, sev Severity, msg string) (int, bool) {
	m.d = append(m.d, &naiveDiag{id: m.next, a: a, b: b, sev: sev, msg: msg})
	m.next++
	return m.next - 1, true
}

// TestRandomDifferential drives Store and the naive model with the same random
// valid edit/registration stream and compares text, coordinates and diagnostics.
func TestRandomDifferential(t *testing.T) {
	var logw io.Writer = nopW{}
	rng := rand.New(rand.NewSource(20261006))
	alphabet := []string{"a", "b", "😀", "\n", "\r", "x"}

	randText := func() string {
		var s string
		for i := 0; i < rng.Intn(6)+1; i++ {
			s += alphabet[rng.Intn(len(alphabet))]
		}
		return s
	}

	for iter := 0; iter < 400; iter++ {
		init := randText()
		store := NewStore(init, WithLogger(logw))
		model := newNaive(init)
		if iter == 0 {
			t.Logf("init=%q", init)
		}

		for step := 0; step < 40; step++ {
			switch rng.Intn(3) {
			case 0, 1:
				// Build a batch of 1-3 non-overlapping valid edits.
				var storeEdits []Edit
				var modelEdits []naiveEdit
				used := []struct{ s, e int }{}
				n := rng.Intn(3) + 1
				ok := true
				for k := 0; k < n; k++ {
					safe := safeOffsets(model.u16)
					if len(safe) == 0 {
						ok = false
						break
					}
					s := safe[rng.Intn(len(safe))]
					e := s
					if rng.Intn(2) == 0 {
						cands := []int{}
						for _, o := range safe {
							if o >= s {
								cands = append(cands, o)
							}
						}
						e = cands[rng.Intn(len(cands))]
					}
					for _, u := range used {
						if e > s && u.e > u.s && s < u.e && u.s < e {
							ok = false
						}
						// Avoid mixing an insertion point with a non-empty edit
						// boundary (keep the linear text rebuild unambiguous);
						// same-point insertions are exercised separately.
						if e == s && u.e > u.s && (s == u.s || s == u.e) {
							ok = false
						}
						// No edit boundary/point may lie strictly inside the
						// other non-empty edit; touching endpoints is allowed.
						if e > s {
							if u.s > s && u.s < e {
								ok = false
							}
							if u.e > s && u.e < e {
								ok = false
							}
						} else if u.e > u.s && s > u.s && s < u.e {
							ok = false
						}
					}
					if ok {
						used = append(used, struct{ s, e int }{s, e})
					}
					txt := randText()
					sp := model.posOf(s)
					ep := model.posOf(e)
					storeEdits = append(storeEdits, Edit{Range: Range{sp, ep}, Text: txt})
					modelEdits = append(modelEdits, naiveEdit{s, e, encode16(txt)})
				}
				if !ok {
					continue
				}
				base := model.ver
				if iter == 0 {
					t.Logf("step%d APPLY base=%d edits=%v store=%v", step, base, modelEdits, storeEdits)
				}
				_, serr := store.Apply(base, storeEdits)
				_, mok := model.apply(base, modelEdits)
				if (serr == nil) != mok {
					t.Fatalf("iter%d step%d accept mismatch store=%v model=%v", iter, step, serr, mok)
				}
				if serr == nil {
					if store.Text() != model.text {
						t.Fatalf("iter%d step%d text store=%q model=%q", iter, step, store.Text(), model.text)
					}
				}
			case 2:
				safe := safeOffsets(model.u16)
				if len(safe) == 0 {
					continue
				}
				a := safe[rng.Intn(len(safe))]
				b := a
				if rng.Intn(2) == 0 {
					rest := safe
					_ = rest
					// pick a safe b >= a
					cands := []int{}
					for _, o := range safe {
						if o >= a {
							cands = append(cands, o)
						}
					}
					b = cands[rng.Intn(len(cands))]
				}
				ap := model.posOf(a)
				bp := model.posOf(b)
				if iter == 0 {
					t.Logf("step%d REG [%d,%d]", step, a, b)
				}
				_, serr := store.Register(model.ver, Diagnostic{
					Range: Range{ap, bp}, Severity: Severity(1 + rng.Intn(4)), Message: "m",
				})
				if serr == nil {
					model.register(a, b, SeverityError, "m")
				}
			}

			// Compare snapshot diagnostics every step.
			if store.Version() == model.ver {
				snap := store.Snapshot()
				if snap.Text != model.text {
					t.Fatalf("iter%d step%d snapshot text mismatch", iter, step)
				}
				var alive []*naiveDiag
				for _, d := range model.d {
					if !d.dead {
						alive = append(alive, d)
					}
				}
				sort.SliceStable(alive, func(i, j int) bool {
					if alive[i].a != alive[j].a {
						return alive[i].a < alive[j].a
					}
					if alive[i].b != alive[j].b {
						return alive[i].b < alive[j].b
					}
					return alive[i].id < alive[j].id
				})
				if iter == 0 && step == 11 {
					for _, n := range store.diags.activeSorted() {
						t.Logf("STOREDIAG a=%d b=%d", n.a, n.b)
					}
					for _, d := range alive {
						t.Logf("MODELDIAG a=%d b=%d", d.a, d.b)
					}
				}
				if len(snap.Diagnostics) != len(alive) {
					t.Fatalf("iter%d step%d active count store=%d model=%d", iter, step,
						len(snap.Diagnostics), len(alive))
				}
				for i, d := range alive {
					wantS := model.posOf(d.a)
					wantE := model.posOf(d.b)
					got := snap.Diagnostics[i].Range
					if got.Start != wantS || got.End != wantE {
						t.Fatalf("iter%d step%d diag#%d id=%d offs(%d,%d) store=%+v model=[%+v,%+v] text=%q",
							iter, step, i, d.id, d.a, d.b, got, wantS, wantE, model.text)
					}
				}
				var dead []*naiveDiag
				for _, d := range model.d {
					if d.dead {
						dead = append(dead, d)
					}
				}
				sort.SliceStable(dead, func(i, j int) bool {
					if dead[i].deadVer != dead[j].deadVer {
						return dead[i].deadVer < dead[j].deadVer
					}
					return dead[i].id < dead[j].id
				})
				if len(snap.Invalidated) != len(dead) {
					t.Fatalf("iter%d step%d invalidated count store=%d model=%d",
						iter, step, len(snap.Invalidated), len(dead))
				}
				for i, d := range dead {
					if snap.Invalidated[i].InvalidatedVersion != d.deadVer {
						t.Fatalf("iter%d step%d dead#%d version store=%d model=%d",
							iter, step, i, snap.Invalidated[i].InvalidatedVersion, d.deadVer)
					}
				}

				// Coordinate round-trips for a few offsets.
				checkOffs := safeOffsets(model.u16)
				pick := func(i int) int { return checkOffs[(i*7+1)%len(checkOffs)] }
				for _, idx := range []int{0, len(checkOffs) / 2, len(checkOffs) - 1} {
					o := pick(idx)
					p, perr := store.PositionOf(o)
					mp := model.posOf(o)
					if perr != nil || p != mp {
						t.Fatalf("iter%d step%d PositionOf(%d) store=%v,%v model=%v text=%q safe=%v",
							iter, step, o, p, perr, mp, model.text, checkOffs)
					}
					back, berr := store.OffsetOf(mp)
					if berr != nil || back != o {
						t.Fatalf("iter%d step%d OffsetOf(%v) =%d,%v want %d",
							iter, step, mp, back, berr, o)
					}
				}
			}
		}
	}
}

type nopW struct{}

func (nopW) Write(p []byte) (int, error) { return len(p), nil }
