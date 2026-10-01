package swiss

type naiveTournament struct {
	r            int
	names        map[string]int
	scores       []int
	byes         []bool
	played       map[[2]int]bool
	started      bool
	round        int
	active       bool
	currentBye   int
	currentPairs []naivePairing
	reported     map[[2]int]bool
}

type naivePairing struct {
	x int
	y int
}

type naiveStanding struct {
	seed          int
	score         int
	opponentScore int
}

func newNaive(r int) *naiveTournament {
	return &naiveTournament{
		r:          r,
		names:      make(map[string]int),
		played:     make(map[[2]int]bool),
		currentBye: -1,
		reported:   make(map[[2]int]bool),
	}
}

func (t *naiveTournament) register(name string) (int, error) {
	if t.started {
		return 0, ErrRegistrationClosed
	}
	if name == "" {
		return 0, ErrEmptyName
	}
	if _, duplicate := t.names[name]; duplicate {
		return 0, ErrDuplicateName
	}
	seed := len(t.scores) + 1
	t.names[name] = seed
	t.scores = append(t.scores, 0)
	t.byes = append(t.byes, false)
	return seed, nil
}

func (t *naiveTournament) pair() ([]naivePairing, int, error) {
	if t.active {
		return nil, 0, ErrRoundInProgress
	}
	if t.round >= t.r {
		return nil, 0, ErrTooManyRounds
	}
	if len(t.scores) < 2 {
		return nil, 0, ErrNotEnoughPlayers
	}

	bye := -1
	if len(t.scores)%2 == 1 {
		for seed := 1; seed <= len(t.scores); seed++ {
			if !t.byes[seed-1] {
				if bye == -1 || t.scores[seed-1] < t.scores[bye-1] ||
					(t.scores[seed-1] == t.scores[bye-1] && seed > bye) {
					bye = seed
				}
			}
		}
		if bye == -1 {
			return nil, 0, ErrNoLegalPairing
		}
	}

	order := make([]int, 0, len(t.scores))
	for seed := 1; seed <= len(t.scores); seed++ {
		if seed != bye {
			order = append(order, seed)
		}
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			left, right := order[i], order[j]
			if t.scores[left-1] < t.scores[right-1] ||
				(t.scores[left-1] == t.scores[right-1] && left > right) {
				order[i], order[j] = order[j], order[i]
			}
		}
	}

	used := make(map[int]bool, len(order))
	pairs := make([]naivePairing, 0, len(order)/2)
	for index, x := range order {
		if used[x] {
			continue
		}
		y := 0
		for _, candidate := range order[index+1:] {
			if !used[candidate] && !t.played[naiveGameKey(x, candidate)] {
				y = candidate
				break
			}
		}
		if y == 0 {
			return nil, 0, ErrNoLegalPairing
		}
		pairs = append(pairs, naivePairing{x: x, y: y})
		used[x] = true
		used[y] = true
	}

	t.started = true
	t.round++
	t.active = true
	t.currentBye = bye
	t.currentPairs = append([]naivePairing(nil), pairs...)
	t.reported = make(map[[2]int]bool, len(pairs))
	if bye != -1 {
		t.byes[bye-1] = true
		t.scores[bye-1] += 2
	}
	return append([]naivePairing(nil), pairs...), bye, nil
}

func (t *naiveTournament) report(a, b, result int) error {
	if !t.active {
		return ErrNoRoundInProgress
	}
	if result != 0 && result != 1 && result != 2 {
		return ErrInvalidResult
	}
	if a < 1 || a > len(t.scores) || b < 1 || b > len(t.scores) || a == b {
		return ErrGameNotInRound
	}
	key := naiveGameKey(a, b)
	found := false
	for _, pair := range t.currentPairs {
		if naiveGameKey(pair.x, pair.y) == key {
			found = true
			break
		}
	}
	if !found {
		return ErrGameNotInRound
	}
	if t.reported[key] {
		return ErrGameAlreadyReported
	}

	t.reported[key] = true
	t.played[key] = true
	t.scores[a-1] += result
	t.scores[b-1] += 2 - result
	if len(t.reported) == len(t.currentPairs) {
		t.active = false
		t.currentPairs = nil
		t.currentBye = -1
	}
	return nil
}

func (t *naiveTournament) standings() []naiveStanding {
	standings := make([]naiveStanding, 0, len(t.scores))
	for seed := 1; seed <= len(t.scores); seed++ {
		opponentScore := 0
		for opponent := 1; opponent <= len(t.scores); opponent++ {
			if opponent != seed && t.played[naiveGameKey(seed, opponent)] {
				opponentScore += t.scores[opponent-1]
			}
		}
		standings = append(standings, naiveStanding{
			seed:          seed,
			score:         t.scores[seed-1],
			opponentScore: opponentScore,
		})
	}
	for i := 0; i < len(standings); i++ {
		for j := i + 1; j < len(standings); j++ {
			left, right := standings[i], standings[j]
			if left.score < right.score ||
				(left.score == right.score && left.opponentScore < right.opponentScore) ||
				(left.score == right.score && left.opponentScore == right.opponentScore && left.seed > right.seed) {
				standings[i], standings[j] = standings[j], standings[i]
			}
		}
	}
	return standings
}

func naiveGameKey(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}
