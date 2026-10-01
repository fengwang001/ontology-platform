package ontology

import (
	"sort"
	"sync"
)

type RejectReason string

const (
	ReasonInvalidTime     RejectReason = "invalid_time"
	ReasonClockWentBack   RejectReason = "clock_went_back"
	ReasonInvalidID       RejectReason = "invalid_id"
	ReasonInvalidRating   RejectReason = "invalid_rating"
	ReasonIDAlreadyExists RejectReason = "id_already_exists"
	ReasonQueueFull       RejectReason = "queue_full"
	ReasonIDNotFound      RejectReason = "id_not_found"
	ReasonAlreadyMatched  RejectReason = "already_matched"
	ReasonAlreadyLeft     RejectReason = "already_left"
	ReasonInvalidW0       RejectReason = "invalid_w0"
	ReasonInvalidG        RejectReason = "invalid_g"
	ReasonInvalidWMax     RejectReason = "invalid_wmax"
	ReasonInvalidCapacity RejectReason = "invalid_capacity"
)

type ValidationError struct {
	Reason RejectReason
}

func (err ValidationError) Error() string {
	return string(err.Reason)
}

type Player struct {
	ID     int64
	Rating int64
	Joined int64
}

type Match struct {
	A         Player
	B         Player
	RatingGap int64
	MatchedAt int64
}

type Lobby struct {
	mu sync.Mutex

	w0       int64
	growth   int64
	wmax     int64
	capacity int

	queue      []Player
	known      map[int64]playerState
	latestNow  int64
	haveLatest bool
}

type playerState int

const (
	stateQueued playerState = iota
	stateMatched
	stateLeft
)

func NewLobby(initialRadius, growthPerTime, maxRadius int64, capacity int) (*Lobby, error) {
	switch {
	case initialRadius < 0:
		return nil, ValidationError{Reason: ReasonInvalidW0}
	case growthPerTime < 0 || growthPerTime > 1_000_000:
		return nil, ValidationError{Reason: ReasonInvalidG}
	case maxRadius < initialRadius:
		return nil, ValidationError{Reason: ReasonInvalidWMax}
	case initialRadius > 1_000_000_000:
		return nil, ValidationError{Reason: ReasonInvalidW0}
	case maxRadius > 1_000_000_000:
		return nil, ValidationError{Reason: ReasonInvalidWMax}
	case capacity < 2:
		return nil, ValidationError{Reason: ReasonInvalidCapacity}
	}

	return &Lobby{
		w0:       initialRadius,
		growth:   growthPerTime,
		wmax:     maxRadius,
		capacity: capacity,
		known:    make(map[int64]playerState),
	}, nil
}

func (lobby *Lobby) Join(id, rating, now int64) error {
	lobby.mu.Lock()
	defer lobby.mu.Unlock()

	switch {
	case now < 0 || now > 1_000_000_000:
		return ValidationError{Reason: ReasonInvalidTime}
	case lobby.haveLatest && now < lobby.latestNow:
		return ValidationError{Reason: ReasonClockWentBack}
	case id < 1:
		return ValidationError{Reason: ReasonInvalidID}
	case rating < 0 || rating > 5000:
		return ValidationError{Reason: ReasonInvalidRating}
	}

	if _, exists := lobby.known[id]; exists {
		return ValidationError{Reason: ReasonIDAlreadyExists}
	}
	if len(lobby.queue) >= lobby.capacity {
		return ValidationError{Reason: ReasonQueueFull}
	}

	player := Player{ID: id, Rating: rating, Joined: now}
	lobby.queue = append(lobby.queue, player)
	sort.Slice(lobby.queue, func(i, j int) bool {
		return playerLess(lobby.queue[i], lobby.queue[j])
	})
	lobby.known[id] = stateQueued
	lobby.setLatestNow(now)
	return nil
}

func (lobby *Lobby) Leave(id, now int64) error {
	lobby.mu.Lock()
	defer lobby.mu.Unlock()

	if err := lobby.validateClock(now); err != nil {
		return err
	}

	state, exists := lobby.known[id]
	switch {
	case !exists:
		return ValidationError{Reason: ReasonIDNotFound}
	case state == stateMatched:
		return ValidationError{Reason: ReasonAlreadyMatched}
	case state == stateLeft:
		return ValidationError{Reason: ReasonAlreadyLeft}
	}

	for index, player := range lobby.queue {
		if player.ID == id {
			lobby.queue = append(lobby.queue[:index], lobby.queue[index+1:]...)
			break
		}
	}
	lobby.known[id] = stateLeft
	lobby.setLatestNow(now)
	return nil
}

func (lobby *Lobby) Tick(now int64) ([]Match, error) {
	lobby.mu.Lock()
	defer lobby.mu.Unlock()

	if err := lobby.validateClock(now); err != nil {
		return nil, err
	}

	sort.Slice(lobby.queue, func(i, j int) bool {
		return playerLess(lobby.queue[i], lobby.queue[j])
	})

	matched := make(map[int64]bool)
	matches := make([]Match, 0)
	for aIndex := 0; aIndex < len(lobby.queue); aIndex++ {
		a := lobby.queue[aIndex]
		if matched[a.ID] {
			continue
		}

		bestIndex := -1
		for bIndex := aIndex + 1; bIndex < len(lobby.queue); bIndex++ {
			b := lobby.queue[bIndex]
			if matched[b.ID] || !lobby.playersAccept(a, b, now) {
				continue
			}
			if bestIndex == -1 || lobby.chooseBefore(b, lobby.queue[bestIndex], a, now) {
				bestIndex = bIndex
			}
		}

		if bestIndex == -1 {
			continue
		}

		b := lobby.queue[bestIndex]
		matched[a.ID] = true
		matched[b.ID] = true
		lobby.known[a.ID] = stateMatched
		lobby.known[b.ID] = stateMatched
		matches = append(matches, Match{
			A:         a,
			B:         b,
			RatingGap: absRatingGap(a.Rating, b.Rating),
			MatchedAt: now,
		})
	}

	if len(matched) > 0 {
		remaining := lobby.queue[:0]
		for _, player := range lobby.queue {
			if !matched[player.ID] {
				remaining = append(remaining, player)
			}
		}
		lobby.queue = remaining
	}

	lobby.setLatestNow(now)
	return matches, nil
}

func (lobby *Lobby) Queue() []Player {
	lobby.mu.Lock()
	defer lobby.mu.Unlock()

	queue := make([]Player, len(lobby.queue))
	copy(queue, lobby.queue)
	return queue
}

func (lobby *Lobby) validateClock(now int64) error {
	if now < 0 || now > 1_000_000_000 {
		return ValidationError{Reason: ReasonInvalidTime}
	}
	if lobby.haveLatest && now < lobby.latestNow {
		return ValidationError{Reason: ReasonClockWentBack}
	}
	return nil
}

func (lobby *Lobby) setLatestNow(now int64) {
	lobby.latestNow = now
	lobby.haveLatest = true
}

func (lobby *Lobby) radiusAt(player Player, now int64) int64 {
	radius := lobby.w0 + lobby.growth*(now-player.Joined)
	if radius > lobby.wmax {
		return lobby.wmax
	}
	return radius
}

func (lobby *Lobby) playersAccept(a, b Player, now int64) bool {
	limit := lobby.radiusAt(a, now)
	if bRadius := lobby.radiusAt(b, now); bRadius < limit {
		limit = bRadius
	}
	return absRatingGap(a.Rating, b.Rating) <= limit
}

func (lobby *Lobby) chooseBefore(candidate, current, a Player, now int64) bool {
	candidateGap := absRatingGap(a.Rating, candidate.Rating)
	currentGap := absRatingGap(a.Rating, current.Rating)
	if candidateGap != currentGap {
		return candidateGap < currentGap
	}
	return playerLess(candidate, current)
}

func playerLess(a, b Player) bool {
	if a.Joined != b.Joined {
		return a.Joined < b.Joined
	}
	return a.ID < b.ID
}

func absRatingGap(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}
