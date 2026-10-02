// Package matchmaking implements a rating-based matchmaking lobby whose
// acceptance window widens with waiting time.
package matchmaking

import (
	"errors"
	"fmt"
	"sync"
)

// Rejection reasons. Every rejected operation returns exactly one of these
// sentinel errors, chosen by the documented priority order, and leaves the
// lobby state untouched.
var (
	ErrInvalidTime    = errors.New("matchmaking: invalid time")
	ErrClockRollback  = errors.New("matchmaking: clock rollback")
	ErrInvalidID      = errors.New("matchmaking: invalid id")
	ErrInvalidRating  = errors.New("matchmaking: invalid rating")
	ErrDuplicateID    = errors.New("matchmaking: duplicate id")
	ErrQueueFull      = errors.New("matchmaking: queue full")
	ErrUnknownID      = errors.New("matchmaking: unknown id")
	ErrAlreadyMatched = errors.New("matchmaking: player already matched")
	ErrAlreadyLeft    = errors.New("matchmaking: player already left")
	ErrInvalidParam   = errors.New("matchmaking: invalid constructor parameter")
)

// Constructor parameter bounds.
const (
	maxW      = 1_000_000_000
	maxG      = 1_000_000
	maxNow    = 1_000_000_000
	maxRating = 5000
	minCap    = 2
)

// Player describes a queued player as returned by Queue.
type Player struct {
	ID     int64
	Rating int64
	Joined int64
}

// Pair describes one match produced by Tick.
type Pair struct {
	A    Player
	B    Player
	Diff int64 // |A.Rating - B.Rating|
	At   int64 // tick time when the pair was formed
}

type playerState int

const (
	stateQueued playerState = iota
	stateMatched
	stateLeft
)

type record struct {
	player Player
	state  playerState
}

// Lobby is a matchmaking queue. All methods are safe for concurrent use;
// the result is equivalent to some serial execution order.
type Lobby struct {
	mu     sync.Mutex
	w0     int64
	g      int64
	wmax   int64
	cap    int64
	maxNow int64 // largest now accepted so far; -1 means none
	seen   map[int64]*record
	order  []int64 // ids currently queued, in (Joined, ID) order
	pairs  []Pair
}

// NewLobby validates the constructor parameters and returns a Lobby.
func NewLobby(w0, g, wmax, cap int64) (*Lobby, error) {
	switch {
	case w0 < 0:
		return nil, fmt.Errorf("%w: W0 < 0", ErrInvalidParam)
	case g < 0 || g > maxG:
		return nil, fmt.Errorf("%w: G out of range", ErrInvalidParam)
	case wmax < w0:
		return nil, fmt.Errorf("%w: Wmax < W0", ErrInvalidParam)
	case w0 > maxW || wmax > maxW:
		return nil, fmt.Errorf("%w: W0 or Wmax too large", ErrInvalidParam)
	case cap < minCap:
		return nil, fmt.Errorf("%w: Cap < %d", ErrInvalidParam, minCap)
	}
	return &Lobby{
		w0:     w0,
		g:      g,
		wmax:   wmax,
		cap:    cap,
		maxNow: -1,
		seen:   make(map[int64]*record),
	}, nil
}

// tolerance returns the acceptance radius of a player who joined at `joined`,
// evaluated at time `now`: min(Wmax, W0 + G*(now-joined)).
func (l *Lobby) tolerance(joined, now int64) int64 {
	w := l.w0 + l.g*(now-joined)
	if w > l.wmax {
		w = l.wmax
	}
	return w
}

// checkTime validates the operation timestamp against the illegal-time and
// clock-rollback rules, in that order.
func (l *Lobby) checkTime(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < l.maxNow {
		return ErrClockRollback
	}
	return nil
}

// insertQueued inserts id into l.order keeping (Joined, ID) order.
func (l *Lobby) insertQueued(id int64) {
	p := l.seen[id].player
	i := 0
	for i < len(l.order) {
		q := l.seen[l.order[i]].player
		if q.Joined > p.Joined || (q.Joined == p.Joined && q.ID > p.ID) {
			break
		}
		i++
	}
	l.order = append(l.order, 0)
	copy(l.order[i+1:], l.order[i:])
	l.order[i] = id
}

// removeQueued removes id from l.order.
func (l *Lobby) removeQueued(id int64) {
	for i, v := range l.order {
		if v == id {
			l.order = append(l.order[:i], l.order[i+1:]...)
			return
		}
	}
}

// Join adds a player to the queue.
func (l *Lobby) Join(id, rating, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if id < 1 {
		return ErrInvalidID
	}
	if rating < 0 || rating > maxRating {
		return ErrInvalidRating
	}
	if _, ok := l.seen[id]; ok {
		return ErrDuplicateID
	}
	if int64(len(l.order)) >= l.cap {
		return ErrQueueFull
	}
	l.seen[id] = &record{player: Player{ID: id, Rating: rating, Joined: now}}
	l.insertQueued(id)
	if now > l.maxNow {
		l.maxNow = now
	}
	return nil
}

// Leave removes a queued player.
func (l *Lobby) Leave(id, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	rec, ok := l.seen[id]
	if !ok {
		return ErrUnknownID
	}
	switch rec.state {
	case stateMatched:
		return ErrAlreadyMatched
	case stateLeft:
		return ErrAlreadyLeft
	}
	rec.state = stateLeft
	l.removeQueued(id)
	if now > l.maxNow {
		l.maxNow = now
	}
	return nil
}

// Tick runs one matching pass and returns the pairs formed, in formation order.
func (l *Lobby) Tick(now int64) ([]Pair, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return nil, err
	}
	matched := make(map[int64]bool, len(l.order))
	var formed []Pair
	// Processing order: l.order is already sorted by (Joined, ID).
	for _, aID := range l.order {
		if matched[aID] {
			continue
		}
		a := l.seen[aID].player
		wa := l.tolerance(a.Joined, now)
		best := Player{}
		bestDiff := int64(0)
		found := false
		for _, bID := range l.order {
			if bID == aID || matched[bID] {
				continue
			}
			b := l.seen[bID].player
			diff := a.Rating - b.Rating
			if diff < 0 {
				diff = -diff
			}
			wb := l.tolerance(b.Joined, now)
			limit := wa
			if wb < limit {
				limit = wb
			}
			if diff > limit {
				continue
			}
			if !found || diff < bestDiff ||
				(diff == bestDiff && (b.Joined < best.Joined ||
					(b.Joined == best.Joined && b.ID < best.ID))) {
				best, bestDiff, found = b, diff, true
			}
		}
		if !found {
			continue
		}
		matched[aID] = true
		matched[best.ID] = true
		formed = append(formed, Pair{A: a, B: best, Diff: bestDiff, At: now})
	}
	for _, p := range formed {
		l.seen[p.A.ID].state = stateMatched
		l.seen[p.B.ID].state = stateMatched
		l.removeQueued(p.A.ID)
		l.removeQueued(p.B.ID)
	}
	l.pairs = append(l.pairs, formed...)
	if now > l.maxNow {
		l.maxNow = now
	}
	return formed, nil
}

// Queue returns the current queue sorted by (Joined, ID).
func (l *Lobby) Queue() []Player {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Player, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, l.seen[id].player)
	}
	return out
}
