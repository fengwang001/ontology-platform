package swiss

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidRounds       = errors.New("swiss: maximum rounds must be between 1 and 20")
	ErrRegistrationClosed  = errors.New("swiss: registration is closed")
	ErrEmptyName           = errors.New("swiss: player name must not be empty")
	ErrDuplicateName       = errors.New("swiss: duplicate player name")
	ErrRoundInProgress     = errors.New("swiss: current round has unreported games")
	ErrTooManyRounds       = errors.New("swiss: maximum number of rounds reached")
	ErrNotEnoughPlayers    = errors.New("swiss: at least two players are required")
	ErrNoLegalPairing      = errors.New("swiss: no legal pairing exists")
	ErrNoRoundInProgress   = errors.New("swiss: no round is in progress")
	ErrInvalidResult       = errors.New("swiss: result must be 0, 1, or 2")
	ErrGameNotInRound      = errors.New("swiss: players are not paired in the current round")
	ErrGameAlreadyReported = errors.New("swiss: game has already been reported")
)

type Tournament struct {
	mu           sync.Mutex
	maxRounds    int
	names        map[string]int
	scores       []int
	byes         []bool
	played       map[[2]int]struct{}
	started      bool
	round        int
	roundActive  bool
	currentBye   int
	currentPairs []Pairing
	reported     map[[2]int]bool
}

type Pairing struct {
	X int
	Y int
}

type Standing struct {
	Seed          int
	Score         int
	OpponentScore int
}

func New(maxRounds int) (*Tournament, error) {
	if maxRounds < 1 || maxRounds > 20 {
		return nil, ErrInvalidRounds
	}
	return &Tournament{
		maxRounds:  maxRounds,
		names:      make(map[string]int),
		played:     make(map[[2]int]struct{}),
		currentBye: -1,
		reported:   make(map[[2]int]bool),
	}, nil
}

func (t *Tournament) Register(name string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return 0, ErrRegistrationClosed
	}
	if name == "" {
		return 0, ErrEmptyName
	}
	if _, exists := t.names[name]; exists {
		return 0, ErrDuplicateName
	}
	seed := len(t.scores) + 1
	t.names[name] = seed
	t.scores = append(t.scores, 0)
	t.byes = append(t.byes, false)
	return seed, nil
}

func (t *Tournament) Pair() ([]Pairing, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.roundActive {
		return nil, 0, ErrRoundInProgress
	}
	if t.round >= t.maxRounds {
		return nil, 0, ErrTooManyRounds
	}
	if len(t.scores) < 2 {
		return nil, 0, ErrNotEnoughPlayers
	}

	bye := -1
	seeds := make([]int, 0, len(t.scores))
	for seed := 1; seed <= len(t.scores); seed++ {
		seeds = append(seeds, seed)
	}
	if len(t.scores)%2 == 1 {
		for _, seed := range seeds {
			if !t.byes[seed-1] && (bye == -1 ||
				t.scores[seed-1] < t.scores[bye-1] ||
				(t.scores[seed-1] == t.scores[bye-1] && seed > bye)) {
				bye = seed
			}
		}
		if bye == -1 {
			return nil, 0, ErrNoLegalPairing
		}
		seeds = append(seeds[:bye-1], seeds[bye:]...)
	}

	sort.Slice(seeds, func(i, j int) bool {
		left := seeds[i] - 1
		right := seeds[j] - 1
		if t.scores[left] != t.scores[right] {
			return t.scores[left] > t.scores[right]
		}
		return seeds[i] < seeds[j]
	})

	paired := make(map[int]bool, len(seeds))
	pairs := make([]Pairing, 0, len(seeds)/2)
	for _, x := range seeds {
		if paired[x] {
			continue
		}
		y := -1
		xIndex := indexOf(seeds, x)
		for _, candidate := range seeds[xIndex+1:] {
			if paired[candidate] {
				continue
			}
			if _, exists := t.played[gameKey(x, candidate)]; !exists {
				y = candidate
				break
			}
		}
		if y == -1 {
			return nil, 0, ErrNoLegalPairing
		}
		pairs = append(pairs, Pairing{X: x, Y: y})
		paired[x] = true
		paired[y] = true
	}

	t.started = true
	t.round++
	t.roundActive = true
	t.currentBye = bye
	t.currentPairs = pairs
	t.reported = make(map[[2]int]bool, len(pairs))
	if bye != -1 {
		t.byes[bye-1] = true
		t.scores[bye-1] += 2
	}

	return append([]Pairing(nil), pairs...), bye, nil
}

func (t *Tournament) Report(a, b, result int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.roundActive {
		return ErrNoRoundInProgress
	}
	if result < 0 || result > 2 {
		return ErrInvalidResult
	}
	if a < 1 || a > len(t.scores) || b < 1 || b > len(t.scores) || a == b {
		return ErrGameNotInRound
	}

	key := gameKey(a, b)
	inCurrentRound := false
	for _, pair := range t.currentPairs {
		if gameKey(pair.X, pair.Y) == key {
			inCurrentRound = true
			break
		}
	}
	if !inCurrentRound {
		return ErrGameNotInRound
	}
	if t.reported[key] {
		return ErrGameAlreadyReported
	}

	t.reported[key] = true
	t.played[key] = struct{}{}
	t.scores[a-1] += result
	t.scores[b-1] += 2 - result

	if len(t.reported) == len(t.currentPairs) {
		t.roundActive = false
		t.currentPairs = nil
		t.currentBye = -1
	}
	return nil
}

func (t *Tournament) Standings() []Standing {
	t.mu.Lock()
	defer t.mu.Unlock()
	standings := make([]Standing, 0, len(t.scores))
	for seed := 1; seed <= len(t.scores); seed++ {
		opponentScore := 0
		for opponent := 1; opponent <= len(t.scores); opponent++ {
			if opponent == seed {
				continue
			}
			if _, exists := t.played[gameKey(seed, opponent)]; exists {
				opponentScore += t.scores[opponent-1]
			}
		}
		standings = append(standings, Standing{
			Seed:          seed,
			Score:         t.scores[seed-1],
			OpponentScore: opponentScore,
		})
	}

	sort.Slice(standings, func(i, j int) bool {
		if standings[i].Score != standings[j].Score {
			return standings[i].Score > standings[j].Score
		}
		if standings[i].OpponentScore != standings[j].OpponentScore {
			return standings[i].OpponentScore > standings[j].OpponentScore
		}
		return standings[i].Seed < standings[j].Seed
	})
	return standings
}

func gameKey(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

func indexOf(values []int, target int) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
