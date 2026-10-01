// Package bracket 实现带轮空与退赛级联晋级的单败淘汰赛对阵树。
package bracket

import (
	"errors"
	"sync"
)

// Kind 表示一场比赛的结果类型。
type Kind int

const (
	// KindOpen 表示该场尚无结果（含未就绪场次）。
	KindOpen Kind = iota
	// KindBye 表示首轮轮空场（一方缺席，对手直接晋级）。
	KindBye
	// KindManual 表示结果由 Report 或 Correct 人工登记。
	KindManual
	// KindTechnical 表示结果由退赛级联自动判定。
	KindTechnical
)

// String 返回类型的可读名称。
func (k Kind) String() string {
	switch k {
	case KindBye:
		return "bye"
	case KindManual:
		return "manual"
	case KindTechnical:
		return "technical"
	default:
		return "open"
	}
}

// Match 描述一场比赛在某一时刻的快照。
type Match struct {
	Round  int  `json:"round"`
	Index  int  `json:"index"`
	Left   int  `json:"left"`   // 左位种子号；0 表示缺席或尚未确定
	Right  int  `json:"right"`  // 右位种子号；0 表示缺席或尚未确定
	Winner int  `json:"winner"` // 胜者种子号；0 表示尚无结果
	Kind   Kind `json:"kind"`
	Ready  bool `json:"ready"`
}

// Bracket 是单败淘汰赛对阵树。
type Bracket struct {
	n          int
	b          int
	rounds     int
	mu         sync.Mutex
	matches    map[[2]int]*match
	withdrawn  map[int]bool
	eliminated map[int]bool
}

type match struct {
	round  int
	index  int
	left   int // 左位种子号；0 表示缺席或尚未确定
	right  int // 右位种子号；0 表示缺席或尚未确定
	winner int
	kind   Kind // KindOpen / KindBye / KindManual / KindTechnical
}

func (m *match) ready() bool {
	return m.kind != KindBye && m.left != 0 && m.right != 0
}

// foldedOrder 返回折叠后的种子位序列：
// order(2)=[1,2]，order(2m) 为对 order(m) 每个 s 依次写出 s 与 2m+1-s。
func foldedOrder(b int) []int {
	order := []int{1, 2}
	for size := 2; size < b; size *= 2 {
		next := make([]int, 0, 2*size)
		for _, s := range order {
			next = append(next, s, 2*size+1-s)
		}
		order = next
	}
	return order
}

// 各类拒绝原因，按规范要求的顺序区分。
var (
	ErrMatchNotFound     = errors.New("bracket: match (r,i) does not exist")
	ErrByeMatch          = errors.New("bracket: first-round bye match cannot be reported")
	ErrAlreadyDecided    = errors.New("bracket: match already has a result")
	ErrNotReady          = errors.New("bracket: match is not ready (both slots must be filled)")
	ErrNotAParticipant   = errors.New("bracket: w is not a participant of the match")
	ErrNoResult          = errors.New("bracket: match has no result yet")
	ErrTechnicalResult   = errors.New("bracket: technical decision cannot be corrected")
	ErrAlreadyWinner     = errors.New("bracket: w is already the current winner")
	ErrNextRoundDecided  = errors.New("bracket: corresponding match in next round already has a result")
	ErrSeedOutOfRange    = errors.New("bracket: seed must be in 1..N")
	ErrAlreadyWithdrawn  = errors.New("bracket: seed already withdrawn")
	ErrAlreadyEliminated = errors.New("bracket: seed has already lost a match")
	ErrChampionDecided   = errors.New("bracket: champion already decided")
)

// New 创建 N 名选手（种子 1..N）的对阵树。N 必须在 2..64。
func New(n int) (*Bracket, error) {
	if n < 2 || n > 64 {
		return nil, ErrSeedOutOfRange
	}
	b := 1
	rounds := 0
	for b < n {
		b *= 2
		rounds++
	}
	br := &Bracket{
		n:          n,
		b:          b,
		rounds:     rounds,
		matches:    make(map[[2]int]*match),
		withdrawn:  make(map[int]bool),
		eliminated: make(map[int]bool),
	}
	positions := foldedOrder(b)
	for i := 0; i < b/2; i++ {
		left := positions[2*i]
		right := positions[2*i+1]
		if left > n {
			left = 0
		}
		if right > n {
			right = 0
		}
		m := &match{round: 1, index: i + 1, left: left, right: right}
		if left == 0 || right == 0 {
			winner := left + right // 恰好一个非零：二者之和即晋级者
			m.kind = KindBye
			m.winner = winner
		}
		br.matches[[2]int{1, i + 1}] = m
	}
	for r := 2; r <= rounds; r++ {
		for i := 1; i <= b>>(uint(r)); i++ {
			br.matches[[2]int{r, i}] = &match{round: r, index: i}
		}
	}
	br.propagate()
	br.cascade()
	return br, nil
}

// propagate 将所有已有结果场次的胜者写入下一轮对应位置。
func (b *Bracket) propagate() {
	for r := 1; r < b.rounds; r++ {
		count := b.b >> uint(r)
		for i := 1; i <= count; i++ {
			m := b.matches[[2]int{r, i}]
			next := b.matches[[2]int{r + 1, (i + 1) / 2}]
			slot := &next.left
			if i%2 == 0 {
				slot = &next.right
			}
			if m.kind != KindOpen && m.winner != 0 {
				*slot = m.winner
			} else {
				*slot = 0
			}
		}
	}
}

// cascade 反复对所有就绪且未登记、且含退赛选手的场次做技术判定，直到无可判定场次。
func (b *Bracket) cascade() {
	for {
		progressed := false
		for r := 1; r <= b.rounds; r++ {
			count := b.b >> uint(r)
			for i := 1; i <= count; i++ {
				m := b.matches[[2]int{r, i}]
				if m.kind != KindOpen || !m.ready() {
					continue
				}
				lw := b.withdrawn[m.left]
				rw := b.withdrawn[m.right]
				if !lw && !rw {
					continue
				}
				winner := m.left
				switch {
				case lw && rw:
					if m.right < m.left {
						winner = m.right
					}
				case rw:
					winner = m.left
				default:
					winner = m.right
				}
				loser := m.right
				if winner == m.right {
					loser = m.left
				}
				m.winner = winner
				m.kind = KindTechnical
				b.eliminated[loser] = true
				if r < b.rounds {
					next := b.matches[[2]int{r + 1, (i + 1) / 2}]
					if i%2 == 1 {
						next.left = winner
					} else {
						next.right = winner
					}
				}
				progressed = true
			}
		}
		if !progressed {
			return
		}
	}
}

func (b *Bracket) exists(r, i int) bool {
	if r < 1 || r > b.rounds || i < 1 || i > b.b>>uint(r) {
		return false
	}
	return true
}

// Report 登记就绪场次 (r,i) 的胜者 w。
func (b *Bracket) Report(r, i, w int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exists(r, i) {
		return ErrMatchNotFound
	}
	m := b.matches[[2]int{r, i}]
	if m.kind == KindBye {
		return ErrByeMatch
	}
	if m.kind != KindOpen {
		return ErrAlreadyDecided
	}
	if !m.ready() {
		return ErrNotReady
	}
	if w != m.left && w != m.right {
		return ErrNotAParticipant
	}
	loser := m.right
	if w == m.right {
		loser = m.left
	}
	m.winner = w
	m.kind = KindManual
	b.eliminated[loser] = true
	b.propagate()
	b.cascade()
	return nil
}

// Correct 将已人工登记场次 (r,i) 的胜者更正为另一名参赛者 w。
func (b *Bracket) Correct(r, i, w int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exists(r, i) {
		return ErrMatchNotFound
	}
	m := b.matches[[2]int{r, i}]
	if m.kind == KindBye {
		return ErrByeMatch
	}
	if m.kind == KindOpen {
		return ErrNoResult
	}
	if m.kind == KindTechnical {
		return ErrTechnicalResult
	}
	if w != m.left && w != m.right {
		return ErrNotAParticipant
	}
	if w == m.winner {
		return ErrAlreadyWinner
	}
	if r < b.rounds {
		next := b.matches[[2]int{r + 1, (i + 1) / 2}]
		if next.kind != KindOpen {
			return ErrNextRoundDecided
		}
	}
	oldWinner := m.winner
	oldLoser := m.right
	if oldWinner == m.right {
		oldLoser = m.left
	}
	delete(b.eliminated, oldLoser)
	m.winner = w
	m.kind = KindManual
	b.eliminated[oldWinner] = true
	b.propagate()
	b.cascade()
	return nil
}

// Withdraw 使选手 s 退赛并留在原位，随后进行级联技术判定。
func (b *Bracket) Withdraw(s int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s < 1 || s > b.n {
		return ErrSeedOutOfRange
	}
	if b.withdrawn[s] {
		return ErrAlreadyWithdrawn
	}
	if b.eliminated[s] {
		return ErrAlreadyEliminated
	}
	final := b.matches[[2]int{b.rounds, 1}]
	if final.kind != KindOpen {
		return ErrChampionDecided
	}
	b.withdrawn[s] = true
	b.cascade()
	return nil
}

// Bracket 返回每场比赛的快照，按轮次与场次顺序排列。
func (b *Bracket) Bracket() []Match {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Match, 0)
	for r := 1; r <= b.rounds; r++ {
		count := b.b >> uint(r)
		for i := 1; i <= count; i++ {
			m := b.matches[[2]int{r, i}]
			out = append(out, Match{
				Round:  m.round,
				Index:  m.index,
				Left:   m.left,
				Right:  m.right,
				Winner: m.winner,
				Kind:   m.kind,
				Ready:  m.ready(),
			})
		}
	}
	return out
}

// Champion 返回冠军；尚未决出时返回 0。
func (b *Bracket) Champion() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	final := b.matches[[2]int{b.rounds, 1}]
	if final.kind == KindOpen {
		return 0
	}
	return final.winner
}
