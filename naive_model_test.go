package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveLobby struct {
	w0       int64
	growth   int64
	wmax     int64
	capacity int
	queue    []Player
	known    map[int64]playerState
	latest   int64
	seen     bool
}

func newNaiveLobby(w0, growth, wmax int64, capacity int) *naiveLobby {
	return &naiveLobby{
		w0:       w0,
		growth:   growth,
		wmax:     wmax,
		capacity: capacity,
		known:    make(map[int64]playerState),
	}
}

func (lobby *naiveLobby) join(id, rating, now int64) (RejectReason, bool) {
	if reason, ok := lobby.validateClock(now); !ok {
		return reason, false
	}
	if id < 1 {
		return ReasonInvalidID, false
	}
	if rating < 0 || rating > 5000 {
		return ReasonInvalidRating, false
	}
	if _, exists := lobby.known[id]; exists {
		return ReasonIDAlreadyExists, false
	}
	if len(lobby.queue) >= lobby.capacity {
		return ReasonQueueFull, false
	}

	player := Player{ID: id, Rating: rating, Joined: now}
	lobby.queue = append(lobby.queue, player)
	lobby.sortQueue()
	lobby.known[id] = stateQueued
	lobby.latest = now
	lobby.seen = true
	return "", true
}

func (lobby *naiveLobby) leave(id, now int64) (RejectReason, bool) {
	if reason, ok := lobby.validateClock(now); !ok {
		return reason, false
	}
	state, exists := lobby.known[id]
	if !exists {
		return ReasonIDNotFound, false
	}
	if state == stateMatched {
		return ReasonAlreadyMatched, false
	}
	if state == stateLeft {
		return ReasonAlreadyLeft, false
	}

	for index, player := range lobby.queue {
		if player.ID == id {
			lobby.queue = append(lobby.queue[:index], lobby.queue[index+1:]...)
			break
		}
	}
	lobby.known[id] = stateLeft
	lobby.latest = now
	lobby.seen = true
	return "", true
}

func (lobby *naiveLobby) tick(now int64) ([]Match, RejectReason, bool) {
	if reason, ok := lobby.validateClock(now); !ok {
		return nil, reason, false
	}

	lobby.sortQueue()
	matchedIDs := make(map[int64]bool)
	matches := make([]Match, 0)
	for ai, a := range lobby.queue {
		if matchedIDs[a.ID] {
			continue
		}

		best := -1
		for bi := ai + 1; bi < len(lobby.queue); bi++ {
			b := lobby.queue[bi]
			if matchedIDs[b.ID] || !lobby.accepts(a, b, now) {
				continue
			}
			if best == -1 || naiveCandidateBetter(a, b, lobby.queue[best]) {
				best = bi
			}
		}
		if best == -1 {
			continue
		}

		b := lobby.queue[best]
		matchedIDs[a.ID] = true
		matchedIDs[b.ID] = true
		lobby.known[a.ID] = stateMatched
		lobby.known[b.ID] = stateMatched
		matches = append(matches, Match{
			A:         a,
			B:         b,
			RatingGap: absRatingGap(a.Rating, b.Rating),
			MatchedAt: now,
		})
	}

	remaining := lobby.queue[:0]
	for _, player := range lobby.queue {
		if !matchedIDs[player.ID] {
			remaining = append(remaining, player)
		}
	}
	lobby.queue = remaining
	lobby.latest = now
	lobby.seen = true
	return matches, "", true
}

func (lobby *naiveLobby) validateClock(now int64) (RejectReason, bool) {
	if now < 0 || now > 1_000_000_000 {
		return ReasonInvalidTime, false
	}
	if lobby.seen && now < lobby.latest {
		return ReasonClockWentBack, false
	}
	return "", true
}

func (lobby *naiveLobby) radius(player Player, now int64) int64 {
	radius := lobby.w0 + lobby.growth*(now-player.Joined)
	if radius > lobby.wmax {
		return lobby.wmax
	}
	return radius
}

func (lobby *naiveLobby) accepts(a, b Player, now int64) bool {
	radius := lobby.radius(a, now)
	if other := lobby.radius(b, now); other < radius {
		radius = other
	}
	return absRatingGap(a.Rating, b.Rating) <= radius
}

func (lobby *naiveLobby) sortQueue() {
	sort.Slice(lobby.queue, func(i, j int) bool {
		return playerLess(lobby.queue[i], lobby.queue[j])
	})
}

func naiveCandidateBetter(a, candidate, current Player) bool {
	candidateGap := absRatingGap(a.Rating, candidate.Rating)
	currentGap := absRatingGap(a.Rating, current.Rating)
	if candidateGap != currentGap {
		return candidateGap < currentGap
	}
	return playerLess(candidate, current)
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w0 := int64(rng.Intn(31))
			growth := int64(rng.Intn(21))
			wmax := w0 + int64(rng.Intn(51))
			capacity := 2 + rng.Intn(7)

			real, err := NewLobby(w0, growth, wmax, capacity)
			if err != nil {
				t.Fatalf("NewLobby(w0=%d,g=%d,wmax=%d,cap=%d): %v", w0, growth, wmax, capacity, err)
			}
			reference := newNaiveLobby(w0, growth, wmax, capacity)
			nextID := int64(1)
			now := int64(0)
			log := []string{fmt.Sprintf("config W0=%d G=%d Wmax=%d Cap=%d", w0, growth, wmax, capacity)}

			for opIndex := 0; opIndex < 80; opIndex++ {
				now += int64(rng.Intn(6))
				opKind := rng.Intn(10)
				var entry string

				switch {
				case opKind < 6:
					id := nextID
					if rng.Intn(6) == 0 {
						id = int64(rng.Intn(4))
					} else {
						nextID++
					}
					rating := int64(rng.Intn(5201) - 100)
					joinNow := now
					if rng.Intn(15) == 0 {
						joinNow = -1
					} else if rng.Intn(15) == 0 {
						joinNow = now - int64(1+rng.Intn(5))
					}

					err := real.Join(id, rating, joinNow)
					reason, accepted := reference.join(id, rating, joinNow)
					entry = fmt.Sprintf("op=%02d Join(id=%d,rating=%d,now=%d) accepted=%v reason=%s queue=%s",
						opIndex, id, rating, joinNow, accepted, reasonOrOK(reason), formatPlayers(reference.queue))
					if !compareError(t, entry, err, reason, accepted) {
						t.Fatalf("%s\nfull log:\n%s", entry, strings.Join(log, "\n"))
					}
				case opKind < 8:
					id := nextID - int64(rng.Intn(int(nextID)+3))
					if id < 1 {
						id = nextID
						nextID++
					}
					err := real.Leave(id, now)
					reason, accepted := reference.leave(id, now)
					entry = fmt.Sprintf("op=%02d Leave(id=%d,now=%d) accepted=%v reason=%s queue=%s",
						opIndex, id, now, accepted, reasonOrOK(reason), formatPlayers(reference.queue))
					if !compareError(t, entry, err, reason, accepted) {
						t.Fatalf("%s\nfull log:\n%s", entry, strings.Join(log, "\n"))
					}
				default:
					before := append([]Player(nil), reference.queue...)
					matches, err := real.Tick(now)
					expected, reason, accepted := reference.tick(now)
					entry = fmt.Sprintf("op=%02d Tick(now=%d) accepted=%v reason=%s before=%s matches=%s queue=%s",
						opIndex, now, accepted, reasonOrOK(reason), formatPlayers(before),
						formatMatchesWithRadii(expected, w0, growth, wmax), formatPlayers(reference.queue))
					if err != nil {
						if !compareError(t, entry, err, reason, accepted) {
							t.Fatalf("%s\nfull log:\n%s", entry, strings.Join(log, "\n"))
						}
					} else if !compareMatches(matches, expected) {
						t.Fatalf("%s\nactual matches=%s\nfull log:\n%s", entry, formatMatchesWithRadii(matches, w0, growth, wmax), strings.Join(log, "\n"))
					}
					if accepted {
						assertNoAcceptablePair(t, reference, now, entry, log)
					}
				}

				log = append(log, entry)
				t.Log(entry)

				if !comparePlayers(real.Queue(), reference.queue) {
					t.Fatalf("queue mismatch after:\n%s\nactual=%s expected=%s\nfull log:\n%s",
						entry, formatPlayers(real.Queue()), formatPlayers(reference.queue), strings.Join(log, "\n"))
				}
			}
		})
	}
}

func compareError(t *testing.T, entry string, err error, expectedReason RejectReason, accepted bool) bool {
	t.Helper()
	if accepted {
		if err != nil {
			t.Errorf("%s: unexpected error %v", entry, err)
			return false
		}
		return true
	}

	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("%s: expected ValidationError(%s), got %v", entry, expectedReason, err)
		return false
	}
	if validationErr.Reason != expectedReason {
		t.Errorf("%s: reason = %s, want %s", entry, validationErr.Reason, expectedReason)
		return false
	}
	return true
}

func compareMatches(actual, expected []Match) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func comparePlayers(actual, expected []Player) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func assertNoAcceptablePair(t *testing.T, lobby *naiveLobby, now int64, entry string, log []string) {
	t.Helper()
	for i := 0; i < len(lobby.queue); i++ {
		for j := i + 1; j < len(lobby.queue); j++ {
			if lobby.accepts(lobby.queue[i], lobby.queue[j], now) {
				t.Fatalf("%s\nacceptable pair remains: %s and %s, radii=%d,%d gap=%d\nfull log:\n%s",
					entry, formatPlayers([]Player{lobby.queue[i]}), formatPlayers([]Player{lobby.queue[j]}),
					lobby.radius(lobby.queue[i], now), lobby.radius(lobby.queue[j], now),
					absRatingGap(lobby.queue[i].Rating, lobby.queue[j].Rating), strings.Join(log, "\n"))
			}
		}
	}
}

func reasonOrOK(reason RejectReason) string {
	if reason == "" {
		return "ok"
	}
	return string(reason)
}

func formatPlayers(players []Player) string {
	if len(players) == 0 {
		return "[]"
	}
	parts := make([]string, len(players))
	for index, player := range players {
		parts[index] = fmt.Sprintf("{id:%d,rating:%d,joined:%d}", player.ID, player.Rating, player.Joined)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatMatchesWithRadii(matches []Match, w0, growth, wmax int64) string {
	if len(matches) == 0 {
		return "[]"
	}
	parts := make([]string, len(matches))
	for index, match := range matches {
		radiusA := cappedRadius(w0, growth, wmax, match.A, match.MatchedAt)
		radiusB := cappedRadius(w0, growth, wmax, match.B, match.MatchedAt)
		parts[index] = fmt.Sprintf("{A:%d,B:%d,gap:%d,at:%d,wa:%d,wb:%d,limit:%d,basis:gap<=min}",
			match.A.ID, match.B.ID, match.RatingGap, match.MatchedAt, radiusA, radiusB, minInt64(radiusA, radiusB))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func cappedRadius(w0, growth, wmax int64, player Player, now int64) int64 {
	radius := w0 + growth*(now-player.Joined)
	if radius > wmax {
		return wmax
	}
	return radius
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
