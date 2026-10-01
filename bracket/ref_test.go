package bracket

// Naive reference implementation of the spec, written independently from the
// production code. Used for exhaustive (all N) and randomized differential
// testing.

type refType int

const (
	refUnresolved refType = iota
	refManual
	refTechnical
	refBye
)

type refMatch struct {
	left, right, winner int
	typ                 refType
}

type refTournament struct {
	n, b, rounds int
	matches      [][]refMatch
	withdrawn    map[int]bool
	eliminated   map[int]bool
}

func naiveOrder(b int) []int {
	o := []int{1, 2}
	for len(o) < b {
		m := len(o)
		nx := make([]int, 0, 2*m)
		for _, s := range o {
			nx = append(nx, s, 2*m+1-s)
		}
		o = nx
	}
	return o
}

func newRef(n int) *refTournament {
	b, rounds := 1, 0
	for b < n {
		b *= 2
		rounds++
	}
	t := &refTournament{
		n: n, b: b, rounds: rounds,
		withdrawn:  map[int]bool{},
		eliminated: map[int]bool{},
	}
	for r := 0; r < rounds; r++ {
		t.matches = append(t.matches, make([]refMatch, b>>(r+1)))
	}
	o := naiveOrder(b)
	for i := 0; i < b/2; i++ {
		l, rr := o[2*i], o[2*i+1]
		m := &t.matches[0][i]
		switch {
		case l > n:
			m.right = rr
			m.winner = rr
			m.typ = refBye
		case rr > n:
			m.left = l
			m.winner = l
			m.typ = refBye
		default:
			m.left, m.right = l, rr
		}
	}
	// Propagate bye winners.
	for r := 0; r < rounds; r++ {
		for i := range t.matches[r] {
			m := &t.matches[r][i]
			if m.typ != refUnresolved {
				t.fill(r, i, m.winner)
			}
		}
	}
	return t
}

// fill places winner of zero-based (r, i) into the next match's slot.
func (t *refTournament) fill(r, i, winner int) {
	if r+1 >= t.rounds {
		return
	}
	nm := &t.matches[r+1][i/2]
	if i%2 == 0 {
		nm.left = winner
	} else {
		nm.right = winner
	}
}

func (t *refTournament) exists(r, i int) bool {
	return r >= 1 && r <= t.rounds && i >= 1 && i <= len(t.matches[r-1])
}

func (t *refTournament) cascade() {
	for {
		done := true
		for r := 0; r < t.rounds; r++ {
			for i := range t.matches[r] {
				m := &t.matches[r][i]
				if m.typ != refUnresolved || m.left == 0 || m.right == 0 {
					continue
				}
				lo, ro := t.withdrawn[m.left], t.withdrawn[m.right]
				if !lo && !ro {
					continue
				}
				w := 0
				switch {
				case lo && ro:
					w = m.left
					if m.right < w {
						w = m.right
					}
				case lo:
					w = m.right
				default:
					w = m.left
				}
				m.winner = w
				m.typ = refTechnical
				if m.left == w {
					t.eliminated[m.right] = true
				} else {
					t.eliminated[m.left] = true
				}
				t.fill(r, i, w)
				done = false
			}
		}
		if done {
			return
		}
	}
}

func refString(err error) string {
	if err == nil {
		return "OK"
	}
	return err.Error()
}

func (t *refTournament) report(r, i, w int) error {
	if !t.exists(r, i) {
		return ErrMatchNotFound
	}
	m := &t.matches[r-1][i-1]
	if r == 1 && m.typ == refBye {
		return ErrByeMatch
	}
	if m.typ != refUnresolved {
		return ErrAlreadyPlayed
	}
	if m.left == 0 || m.right == 0 {
		return ErrNotReady
	}
	if w != m.left && w != m.right {
		return ErrNotContestant
	}
	m.winner = w
	m.typ = refManual
	if m.left == w {
		t.eliminated[m.right] = true
	} else {
		t.eliminated[m.left] = true
	}
	t.fill(r-1, i-1, w)
	t.cascade()
	return nil
}

func (t *refTournament) correct(r, i, w int) error {
	if !t.exists(r, i) {
		return ErrMatchNotFound
	}
	m := &t.matches[r-1][i-1]
	if m.typ == refBye {
		return ErrByeMatch
	}
	if m.typ == refUnresolved {
		return ErrNoResult
	}
	if m.typ == refTechnical {
		return ErrTechnicalMatch
	}
	if w != m.left && w != m.right {
		return ErrNotContestant
	}
	if w == m.winner {
		return ErrAlreadyWinner
	}
	if r < t.rounds && t.matches[r][(i-1)/2].typ != refUnresolved {
		return ErrNextHasResult
	}
	old := m.winner
	m.winner = w
	t.eliminated[old] = true
	t.eliminated[w] = false
	if r < t.rounds {
		nm := &t.matches[r][(i-1)/2]
		if i%2 == 1 {
			nm.left = w
		} else {
			nm.right = w
		}
	}
	t.cascade()
	return nil
}

func (t *refTournament) withdraw(s int) error {
	if s < 1 || s > t.n {
		return ErrSeedOutOfRange
	}
	if t.withdrawn[s] {
		return ErrAlreadyOut
	}
	if t.eliminated[s] {
		return ErrEliminated
	}
	if t.matches[t.rounds-1][0].typ != refUnresolved {
		return ErrChampionCrowned
	}
	t.withdrawn[s] = true
	t.cascade()
	return nil
}

func (t *refTournament) championForTest() (int, bool) {
	m := t.matches[t.rounds-1][0]
	if m.typ == refUnresolved {
		return 0, false
	}
	return m.winner, true
}
