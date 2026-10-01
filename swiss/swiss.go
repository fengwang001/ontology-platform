// Package swiss 实现瑞士制轮次配对与积分登记器。
package swiss

import (
	"errors"
	"sort"
	"sync"
)

// 各类操作按规则顺序返回的哨兵错误。
var (
	ErrStarted      = errors.New("swiss: pairing has started, registration is closed")
	ErrEmptyName    = errors.New("swiss: player name must not be empty")
	ErrDuplicate    = errors.New("swiss: player name already registered")
	ErrPending      = errors.New("swiss: current round has unreported games")
	ErrMaxRounds    = errors.New("swiss: maximum number of rounds reached")
	ErrTooFew       = errors.New("swiss: need at least two players")
	ErrNoPairing    = errors.New("swiss: no legal pairing exists")
	ErrNoRound      = errors.New("swiss: no round in progress")
	ErrBadResult    = errors.New("swiss: result must be 0, 1 or 2")
	ErrUnknownPair  = errors.New("swiss: pair is not a game of the current round")
	ErrAlreadyScore = errors.New("swiss: game already reported")
)

// Pair 表示一轮中的对阵（x、y 均为种子号）。
type Pair struct {
	X int
	Y int
}

// Row 表示积分榜中的一行：种子号、积分、对手分。
type Row struct {
	Seed  int
	Score int
	Buch  int
}

// Tournament 是瑞士制比赛。零值不可用，请使用 New 构造。
// 所有方法均以同一把互斥锁串行化，并发调用的结果等价于某个串行顺序。
type Tournament struct {
	mu sync.Mutex

	maxRounds int
	started   bool // 第一次 Pair 成功后置为 true

	names   []string // 下标为种子号-1
	nameSet map[string]struct{}

	score   []int // 当前积分（含已开始轮次中的轮空分）
	hadBye  []bool
	played  [][]bool // played[i][j]：种子号 i+1 与 j+1 是否交手过
	oppList [][]int  // 每名选手的真实对手（种子号，按交手顺序）

	roundsPlayed int
	inRound      bool
	curPairs     []Pair
	curBye       int // 0 表示本轮无轮空
	reported     []bool
}

// New 创建最多进行 r 轮的比赛；r 必须在 1 到 20 之间。
func New(r int) (*Tournament, error) {
	if r < 1 || r > 20 {
		return nil, errors.New("swiss: max rounds must be between 1 and 20")
	}
	return &Tournament{
		maxRounds: r,
		nameSet:   make(map[string]struct{}),
	}, nil
}

// Register 登记一名选手，返回其种子号（从 1 起）。
// 拒绝顺序：已开始 -> 名称为空 -> 名称重复。
func (t *Tournament) Register(name string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return 0, ErrStarted
	}
	if name == "" {
		return 0, ErrEmptyName
	}
	if _, ok := t.nameSet[name]; ok {
		return 0, ErrDuplicate
	}

	seed := len(t.names) + 1
	t.names = append(t.names, name)
	t.nameSet[name] = struct{}{}
	t.score = append(t.score, 0)
	t.hadBye = append(t.hadBye, false)
	t.oppList = append(t.oppList, nil)
	for i := range t.played {
		t.played[i] = append(t.played[i], false)
	}
	t.played = append(t.played, make([]bool, seed))
	return seed, nil
}

// Pair 开始新一轮，返回 (对阵列表, 轮空者种子号)。
// 选手数为偶数时轮空者为 0（表示无轮空）。
// 拒绝顺序：本轮还有未登记的盘 -> 已进行 R 轮 -> 选手少于 2 人 -> 找不到合法配对。
func (t *Tournament) Pair() ([]Pair, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.inRound {
		return nil, 0, ErrPending
	}
	if t.roundsPlayed >= t.maxRounds {
		return nil, 0, ErrMaxRounds
	}
	n := len(t.names)
	if n < 2 {
		return nil, 0, ErrTooFew
	}

	active := make([]int, 0, n) // 参与配对的选手（0-based）
	bye := -1
	if n%2 == 1 {
		bye = t.pickBye()
		if bye < 0 {
			return nil, 0, ErrNoPairing
		}
		for i := 0; i < n; i++ {
			if i != bye {
				active = append(active, i)
			}
		}
	} else {
		for i := 0; i < n; i++ {
			active = append(active, i)
		}
	}

	// 按（积分降序, 种子号升序）排列。
	sort.SliceStable(active, func(i, j int) bool {
		if t.score[active[i]] != t.score[active[j]] {
			return t.score[active[i]] > t.score[active[j]]
		}
		return active[i] < active[j]
	})

	pairs, ok := t.greedyPairs(active)
	if !ok {
		// 失败的 Pair 不产生轮空记录。
		return nil, 0, ErrNoPairing
	}

	t.started = true
	t.roundsPlayed++
	t.inRound = true
	t.curPairs = pairs
	t.reported = make([]bool, len(pairs))
	if bye >= 0 {
		t.curBye = bye + 1
		t.hadBye[bye] = true
		t.score[bye] += 2 // 轮空记 2 分，视同一场胜利，但不算对手
	} else {
		t.curBye = 0
	}

	out := make([]Pair, len(pairs))
	copy(out, pairs)
	return out, t.curBye, nil
}

// pickBye 必须在持锁状态下调用。
// 从尚未轮空过的选手中选积分最低者，积分相同取种子号最大者。
// 返回 0-based 种子下标；无人可轮空时返回 -1。
func (t *Tournament) pickBye() int {
	choice := -1
	for i := range t.names {
		if t.hadBye[i] {
			continue
		}
		if choice < 0 {
			choice = i
			continue
		}
		if t.score[i] < t.score[choice] ||
			(t.score[i] == t.score[choice] && i > choice) {
			choice = i
		}
	}
	return choice
}

// greedyPairs 必须在持锁状态下调用。
// seq 已按（积分降序, 种子号升序）排好；反复取第一个未配对选手 x，
// 在其后的选手中取第一个未配对且从未交手的 y 与之成对，不回溯。
// 返回的 Pair 使用 1-based 种子号。
func (t *Tournament) greedyPairs(seq []int) ([]Pair, bool) {
	used := make([]bool, len(seq))
	pairs := make([]Pair, 0, len(seq)/2)
	for i := 0; i < len(seq); i++ {
		if used[i] {
			continue
		}
		x := seq[i]
		j := -1
		for k := i + 1; k < len(seq); k++ {
			if used[k] {
				continue
			}
			y := seq[k]
			if !t.played[x][y] {
				j = k
				break
			}
		}
		if j < 0 {
			return nil, false
		}
		used[i] = true
		used[j] = true
		pairs = append(pairs, Pair{X: x + 1, Y: seq[j] + 1})
	}
	return pairs, true
}

// Report 登记当前轮中种子号 a、b 这一盘的结果。
// r 从 a 的角度取 2（a 胜）、1（平）、0（a 负），b 得 2-r。
// 拒绝顺序：当前没有进行中的轮 -> r 非法 -> 不是本轮的一盘 -> 该盘已登记。
func (t *Tournament) Report(a, b, r int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.inRound {
		return ErrNoRound
	}
	if r < 0 || r > 2 {
		return ErrBadResult
	}

	idx := -1
	for i, p := range t.curPairs {
		if (p.X == a && p.Y == b) || (p.X == b && p.Y == a) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrUnknownPair
	}
	if t.reported[idx] {
		return ErrAlreadyScore
	}

	t.reported[idx] = true

	// r 始终从调用方 a 的角度计分；a、b 可以按对阵的任一方向传入。
	ai, bi := a-1, b-1
	t.score[ai] += r
	t.score[bi] += 2 - r
	t.played[ai][bi] = true
	t.played[bi][ai] = true
	t.oppList[ai] = append(t.oppList[ai], b)
	t.oppList[bi] = append(t.oppList[bi], a)

	if t.allReported() {
		t.inRound = false
	}
	return nil
}

// allReported 必须在持锁状态下调用。
func (t *Tournament) allReported() bool {
	for _, done := range t.reported {
		if !done {
			return false
		}
	}
	return true
}

// Standings 返回按（积分降序, 对手分降序, 种子号升序）排序的积分榜。
// 对手分 = 该选手所有真实对手当前积分之和（轮空不计对手）。
func (t *Tournament) Standings() []Row {
	t.mu.Lock()
	defer t.mu.Unlock()

	rows := make([]Row, len(t.names))
	for i := range t.names {
		buch := 0
		for _, opp := range t.oppList[i] {
			buch += t.score[opp-1]
		}
		rows[i] = Row{Seed: i + 1, Score: t.score[i], Buch: buch}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		if rows[i].Buch != rows[j].Buch {
			return rows[i].Buch > rows[j].Buch
		}
		return rows[i].Seed < rows[j].Seed
	})
	return rows
}
