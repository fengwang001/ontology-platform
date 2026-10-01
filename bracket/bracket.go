// Package bracket implements a single-elimination tournament bracket with
// byes, withdrawals and cascading technical decisions.
package bracket

import "sync"

// ResultType describes how a match winner was decided.
type ResultType int

const (
	// Unresolved means the match has no winner yet.
	Unresolved ResultType = iota
	// Manual means the winner was registered (or corrected) by hand.
	Manual
	// Technical means the winner was decided automatically after a withdrawal.
	Technical
	// Bye means the match was a first-round bye and cannot be reported.
	Bye
)

// Match describes one match of the bracket.
type Match struct {
	Round  int
	Index  int
	Left   int // 0 when the slot is not filled yet
	Right  int // 0 when the slot is not filled yet
	Winner int // 0 while unresolved
	Type   ResultType
}

// Tournament is a single-elimination bracket.
type Tournament struct {
	mu         sync.Mutex
	n          int
	b          int
	numRounds  int
	matches    [][]match // matches[r-1][i-1]; zero value = unresolved empty match
	withdrawn  []bool    // indexed by seed
	eliminated []bool    // indexed by seed
}

// New creates a tournament for seeds 1..n.
func New(n int) (*Tournament, error) {
	if n < 2 || n > 64 {
		return nil, ErrInvalidN
	}
	b := 1
	numRounds := 0
	for b < n {
		b <<= 1
		numRounds++
	}
	t := &Tournament{
		n:          n,
		b:          b,
		numRounds:  numRounds,
		matches:    make([][]match, numRounds),
		withdrawn:  make([]bool, n+1),
		eliminated: make([]bool, n+1),
	}
	for r := 0; r < numRounds; r++ {
		t.matches[r] = make([]match, b>>uint(r+1))
	}
	// Seed the first round from order(B) in pairs.
	order := foldedOrder(b)
	for i := 0; i < b/2; i++ {
		left := order[2*i]
		right := order[2*i+1]
		m := &t.matches[0][i]
		if left > n {
			m.right = right
			m.winner = right
			m.typ = Bye
		} else if right > n {
			m.left = left
			m.winner = left
			m.typ = Bye
		} else {
			m.left = left
			m.right = right
		}
	}
	// Propagate bye winners so later-round slots are pre-filled.
	t.propagateAll()
	return t, nil
}

// Report records winner w of match (r, i).
func (t *Tournament) Report(r, i, w int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	m, err := t.lookup(r, i)
	if err != nil {
		return err
	}
	if r == 1 && m.typ == Bye {
		return ErrByeMatch
	}
	if m.typ != Unresolved {
		return ErrAlreadyPlayed
	}
	if m.left == 0 || m.right == 0 {
		return ErrNotReady
	}
	if w != m.left && w != m.right {
		return ErrNotContestant
	}
	m.winner = w
	m.typ = Manual
	t.markLoser(m, w)
	t.advance(r-1, i-1, w)
	t.cascade()
	return nil
}

// Correct changes an already manually reported winner.
func (t *Tournament) Correct(r, i, w int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	m, err := t.lookup(r, i)
	if err != nil {
		return err
	}
	if m.typ == Bye {
		return ErrByeMatch
	}
	if m.typ == Unresolved {
		return ErrNoResult
	}
	if m.typ == Technical {
		return ErrTechnicalMatch
	}
	if w != m.left && w != m.right {
		return ErrNotContestant
	}
	if w == m.winner {
		return ErrAlreadyWinner
	}
	if r < t.numRounds {
		next := &t.matches[r][(i-1)/2]
		if next.typ != Unresolved {
			return ErrNextHasResult
		}
	}
	oldWinner := m.winner
	m.winner = w
	m.typ = Manual
	// Swap elimination flags: the old winner is now the loser and vice versa.
	t.eliminated[oldWinner] = true
	t.eliminated[w] = false
	// Put the new winner into the same slot of the next-round match.
	if r < t.numRounds {
		next := &t.matches[r][(i-1)/2] // r is 1-based: matches[r] is the next round
		if i%2 == 1 {
			next.left = w
		} else {
			next.right = w
		}
	}
	t.cascade()
	return nil
}

// Withdraw marks seed s as withdrawn.
func (t *Tournament) Withdraw(s int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s < 1 || s > t.n {
		return ErrSeedOutOfRange
	}
	if t.withdrawn[s] {
		return ErrAlreadyOut
	}
	if t.eliminated[s] {
		return ErrEliminated
	}
	if _, ok := t.championLocked(); ok {
		return ErrChampionCrowned
	}
	t.withdrawn[s] = true
	t.cascade()
	return nil
}

// Bracket returns a snapshot of every match, round by round.
func (t *Tournament) Bracket() [][]Match {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([][]Match, t.numRounds)
	for r := range t.matches {
		out[r] = make([]Match, len(t.matches[r]))
		for i := range t.matches[r] {
			src := &t.matches[r][i]
			out[r][i] = Match{
				Round:  r + 1,
				Index:  i + 1,
				Left:   src.left,
				Right:  src.right,
				Winner: src.winner,
				Type:   src.typ,
			}
		}
	}
	return out
}

// Champion returns the champion seed and true once the final has a result.
func (t *Tournament) Champion() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.championLocked()
}

// match is one node of the bracket tree.
type match struct {
	left   int // 0 while the slot is not filled
	right  int
	winner int // 0 while unresolved
	typ    ResultType
}

func (t *Tournament) lookup(r, i int) (*match, error) {
	if r < 1 || r > t.numRounds || i < 1 || i > len(t.matches[r-1]) {
		return nil, ErrMatchNotFound
	}
	return &t.matches[r-1][i-1], nil
}

func (t *Tournament) championLocked() (int, bool) {
	final := &t.matches[t.numRounds-1][0]
	if final.typ == Unresolved {
		return 0, false
	}
	return final.winner, true
}

func (t *Tournament) markLoser(m *match, winner int) {
	if m.left == winner {
		t.eliminated[m.right] = true
	} else {
		t.eliminated[m.left] = true
	}
}

// advance places the winner of zero-based match (r, i) into the correct slot
// of the next-round match.
func (t *Tournament) advance(r, i, winner int) {
	if r+1 >= t.numRounds {
		return
	}
	next := &t.matches[r+1][i/2]
	if i%2 == 0 {
		next.left = winner
	} else {
		next.right = winner
	}
}

// propagateAll fills later-round slots from all already decided matches
// (used at construction for the bye winners).
func (t *Tournament) propagateAll() {
	for r := 1; r <= t.numRounds; r++ {
		for i := range t.matches[r-1] {
			m := &t.matches[r-1][i]
			if m.typ != Unresolved {
				t.advance(r-1, i, m.winner)
			}
		}
	}
}

// cascade repeatedly auto-decides every ready, unresolved match containing a
// withdrawn contestant, propagating each technical decision until no match can
// be decided anymore.
func (t *Tournament) cascade() {
	for {
		progress := false
		for r := 1; r <= t.numRounds; r++ {
			for i := range t.matches[r-1] {
				m := &t.matches[r-1][i]
				if m.typ != Unresolved || m.left == 0 || m.right == 0 {
					continue
				}
				leftOut := t.withdrawn[m.left]
				rightOut := t.withdrawn[m.right]
				if !leftOut && !rightOut {
					continue
				}
				var winner int
				switch {
				case leftOut && rightOut:
					winner = m.left
					if m.right < m.left {
						winner = m.right
					}
				case leftOut:
					winner = m.right
				default:
					winner = m.left
				}
				m.winner = winner
				m.typ = Technical
				t.markLoser(m, winner)
				t.advance(r-1, i, winner)
				progress = true
			}
		}
		if !progress {
			return
		}
	}
}

// foldedOrder returns order(B): order(2) = [1,2]; order(2m) interleaves each s
// of order(m) with 2m+1-s.
func foldedOrder(b int) []int {
	order := []int{1, 2}
	for len(order) < b {
		m := len(order)
		next := make([]int, 0, 2*m)
		for _, s := range order {
			next = append(next, s, 2*m+1-s)
		}
		order = next
	}
	return order
}
