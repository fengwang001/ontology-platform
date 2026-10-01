package bracket

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// refBracket 是完全按题目规则独立写成的朴素参考实现：
// 每场参赛者都由 feeder 结果即时重算，结果只存人工/技术两种（轮空由缺席位推出）。
type refBracket struct {
	n, b, rounds int
	withdrawn    map[int]bool
	eliminated   map[int]bool
	winner       map[[2]int]int
	manual       map[[2]int]bool
	left         map[[2]int]int
	right        map[[2]int]int
	isBye        map[[2]int]bool
}

func refFolded(size int) []int {
	order := []int{1, 2}
	for m := 2; m < size; m *= 2 {
		next := make([]int, 0, 2*m)
		for _, s := range order {
			next = append(next, s, 2*m+1-s)
		}
		order = next
	}
	return order
}

func newRef(n int) *refBracket {
	size, rounds := 1, 0
	for size < n {
		size, rounds = size*2, rounds+1
	}
	r := &refBracket{
		n: n, b: size, rounds: rounds,
		withdrawn:  map[int]bool{},
		eliminated: map[int]bool{},
		winner:     map[[2]int]int{},
		manual:     map[[2]int]bool{},
		left:       map[[2]int]int{},
		right:      map[[2]int]int{},
		isBye:      map[[2]int]bool{},
	}
	r.rebuild()
	r.cascade()
	return r
}

// rebuild 按轮次自底向上重算每场参赛者，O(B)。
func (r *refBracket) rebuild() {
	pos := refFolded(r.b)
	for i := 1; i <= r.b/2; i++ {
		l := pos[2*(i-1)]
		rg := pos[2*(i-1)+1]
		if l > r.n {
			l = 0
		}
		if rg > r.n {
			rg = 0
		}
		key := [2]int{1, i}
		r.left[key], r.right[key] = l, rg
		r.isBye[key] = l == 0 || rg == 0
	}
	for rr := 2; rr <= r.rounds; rr++ {
		for i := 1; i <= r.b>>uint(rr); i++ {
			key := [2]int{rr, i}
			r.left[key] = r.feederWinner(rr-1, 2*i-1)
			r.right[key] = r.feederWinner(rr-1, 2*i)
			r.isBye[key] = false
		}
	}
}

func (r *refBracket) exists(rr, i int) bool {
	return rr >= 1 && rr <= r.rounds && i >= 1 && i <= r.b>>uint(rr)
}

func (r *refBracket) participant(rr, i int) (int, int, bool) {
	key := [2]int{rr, i}
	return r.left[key], r.right[key], r.isBye[key]
}

func (r *refBracket) feederWinner(rr, i int) int {
	left, right, bye := r.participant(rr, i)
	if rr == 1 && bye {
		return left + right
	}
	if w, ok := r.winner[[2]int{rr, i}]; ok {
		return w
	}
	return 0
}

func (r *refBracket) ready(rr, i int) bool {
	left, right, bye := r.participant(rr, i)
	return !(rr == 1 && bye) && left != 0 && right != 0
}

func (r *refBracket) kind(rr, i int) Kind {
	_, _, bye := r.participant(rr, i)
	if rr == 1 && bye {
		return KindBye
	}
	key := [2]int{rr, i}
	if _, ok := r.winner[key]; !ok {
		return KindOpen
	}
	if r.manual[key] {
		return KindManual
	}
	return KindTechnical
}

func (r *refBracket) cascade() {
	for {
		r.rebuild()
		progress := false
		for rr := 1; rr <= r.rounds; rr++ {
			for i := 1; i <= r.b>>uint(rr); i++ {
				key := [2]int{rr, i}
				if _, done := r.winner[key]; done {
					continue
				}
				if rr == 1 {
					if _, _, bye := r.participant(rr, i); bye {
						continue
					}
				}
				if !r.ready(rr, i) {
					continue
				}
				left, right, _ := r.participant(rr, i)
				if !r.withdrawn[left] && !r.withdrawn[right] {
					continue
				}
				win := left
				switch {
				case r.withdrawn[left] && r.withdrawn[right]:
					if right < left {
						win = right
					}
				case r.withdrawn[left]:
					win = right
				default:
					win = left
				}
				r.winner[key] = win
				r.manual[key] = false
				r.eliminated[left+right-win] = true
				progress = true
			}
		}
		if !progress {
			return
		}
	}
}

func (r *refBracket) report(rr, i, w int) error {
	if !r.exists(rr, i) {
		return ErrMatchNotFound
	}
	if rr == 1 {
		if _, _, bye := r.participant(rr, i); bye {
			return ErrByeMatch
		}
	}
	key := [2]int{rr, i}
	if _, ok := r.winner[key]; ok {
		return ErrAlreadyDecided
	}
	if !r.ready(rr, i) {
		return ErrNotReady
	}
	left, right, _ := r.participant(rr, i)
	if w != left && w != right {
		return ErrNotAParticipant
	}
	r.winner[key] = w
	r.manual[key] = true
	r.eliminated[left+right-w] = true
	r.cascade()
	return nil
}

func (r *refBracket) correct(rr, i, w int) error {
	if !r.exists(rr, i) {
		return ErrMatchNotFound
	}
	if rr == 1 {
		if _, _, bye := r.participant(rr, i); bye {
			return ErrByeMatch
		}
	}
	key := [2]int{rr, i}
	old, ok := r.winner[key]
	if !ok {
		return ErrNoResult
	}
	if !r.manual[key] {
		return ErrTechnicalResult
	}
	left, right, _ := r.participant(rr, i)
	if w != left && w != right {
		return ErrNotAParticipant
	}
	if w == old {
		return ErrAlreadyWinner
	}
	if rr < r.rounds {
		if _, done := r.winner[[2]int{rr + 1, (i + 1) / 2}]; done {
			return ErrNextRoundDecided
		}
	}
	delete(r.eliminated, left+right-old)
	r.winner[key] = w
	r.manual[key] = true
	r.eliminated[old] = true
	r.cascade()
	return nil
}

func (r *refBracket) withdraw(s int) error {
	if s < 1 || s > r.n {
		return ErrSeedOutOfRange
	}
	if r.withdrawn[s] {
		return ErrAlreadyWithdrawn
	}
	if r.eliminated[s] {
		return ErrAlreadyEliminated
	}
	if r.feederWinner(r.rounds, 1) != 0 {
		return ErrChampionDecided
	}
	r.withdrawn[s] = true
	r.cascade()
	return nil
}

type treeSnapshot struct {
	rows      string
	champion  int
	withdrawn string
}

// fingerprint 是比全树字符串便宜的逐步比较指纹：记录所有有结果/有选手场次的关键信息。
type fingerprint struct {
	s string
	c int
	w string
}

func refSnapshot(r *refBracket) treeSnapshot {
	var b strings.Builder
	for rr := 1; rr <= r.rounds; rr++ {
		for i := 1; i <= r.b>>uint(rr); i++ {
			left, right, _ := r.participant(rr, i)
			win := 0
			if k := r.kind(rr, i); k == KindManual || k == KindTechnical {
				win = r.feederWinner(rr, i)
			}
			if k := r.kind(rr, i); k == KindBye {
				win = r.feederWinner(rr, i)
			}
			fmt.Fprintf(&b, "r%d#%d:%d/%d w=%d %s ready=%v\n",
				rr, i, left, right, win, r.kind(rr, i), r.ready(rr, i))
		}
	}
	withdrawn := []int{}
	for s := 1; s <= r.n; s++ {
		if r.withdrawn[s] {
			withdrawn = append(withdrawn, s)
		}
	}
	return treeSnapshot{b.String(), r.feederWinner(r.rounds, 1), fmt.Sprint(withdrawn)}
}

func prodSnapshot(p *Bracket) treeSnapshot {
	var b strings.Builder
	for _, m := range p.Bracket() {
		fmt.Fprintf(&b, "r%d#%d:%d/%d w=%d %s ready=%v\n",
			m.Round, m.Index, m.Left, m.Right, m.Winner, m.Kind, m.Ready)
	}
	withdrawn := []int{}
	for s := 1; s <= p.n; s++ {
		if p.withdrawn[s] {
			withdrawn = append(withdrawn, s)
		}
	}
	return treeSnapshot{b.String(), p.Champion(), fmt.Sprint(withdrawn)}
}

func (r *refBracket) fingerprint() fingerprint {
	var b strings.Builder
	for rr := 1; rr <= r.rounds; rr++ {
		for i := 1; i <= r.b>>uint(rr); i++ {
			left, right, _ := r.participant(rr, i)
			win := 0
			k := r.kind(rr, i)
			if k == KindManual || k == KindTechnical || k == KindBye {
				win = r.feederWinner(rr, i)
			}
			fmt.Fprintf(&b, "%d,%d,%d,%d,%d,%d,%t|",
				rr, i, left, right, win, k, r.ready(rr, i))
		}
	}
	return fingerprint{b.String(), r.feederWinner(r.rounds, 1), ""}
}

func (p *Bracket) fingerprint() fingerprint {
	var b strings.Builder
	p.mu.Lock()
	defer p.mu.Unlock()
	for rr := 1; rr <= p.rounds; rr++ {
		for i := 1; i <= p.b>>uint(rr); i++ {
			m := p.matches[[2]int{rr, i}]
			fmt.Fprintf(&b, "%d,%d,%d,%d,%d,%d,%t|",
				rr, i, m.left, m.right, m.winner, m.kind, m.ready())
		}
	}
	champ := 0
	if final := p.matches[[2]int{p.rounds, 1}]; final.kind != KindOpen {
		champ = final.winner
	}
	return fingerprint{b.String(), champ, ""}
}

// TestExhaustiveInitialTreeVsRef：N=2..64 逐场与 order(B) 公式及参考实现对照。
func TestExhaustiveInitialTreeVsRef(t *testing.T) {
	for n := 2; n <= 64; n++ {
		prod, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		ref := newRef(n)
		if ps, rs := prodSnapshot(prod), refSnapshot(ref); ps != rs {
			t.Fatalf("N=%d initial tree mismatch:\nPROD\n%s\nREF\n%s", n, ps.rows, rs.rows)
		}
		pos := foldedOrder(ref.b)
		for i := 1; i <= ref.b/2; i++ {
			left, right := pos[2*i-2], pos[2*i-1]
			if left > n {
				left = 0
			}
			if right > n {
				right = 0
			}
			m := getm(prod, 1, i)
			if m.Left != left || m.Right != right {
				t.Fatalf("N=%d (1,%d)=%d/%d want %d/%d", n, i, m.Left, m.Right, left, right)
			}
			isBye := left == 0 || right == 0
			if isBye != (m.Kind == KindBye) {
				t.Fatalf("N=%d (1,%d) bye mismatch", n, i)
			}
			if isBye && (m.Winner != left+right) {
				t.Fatalf("N=%d (1,%d) bye winner=%d want %d", n, i, m.Winner, left+right)
			}
		}
		// 种子 1 与 2 在决赛前不可能同场（折叠种子位保证）。
		for _, m := range prod.Bracket() {
			if m.Round < prod.rounds {
				one := m.Left == 1 || m.Right == 1
				two := m.Left == 2 || m.Right == 2
				if one && two {
					t.Fatalf("N=%d seed 1,2 meet early at r%d#%d", n, m.Round, m.Index)
				}
			}
		}
	}
}

type fuzzOp struct {
	name    string
	r, i, w int
}

func (o fuzzOp) String() string {
	switch o.name {
	case "R":
		return fmt.Sprintf("Report(r=%d,i=%d,w=%d)", o.r, o.i, o.w)
	case "C":
		return fmt.Sprintf("Correct(r=%d,i=%d,w=%d)", o.r, o.i, o.w)
	default:
		return fmt.Sprintf("Withdraw(s=%d)", o.w)
	}
}

func applyFuzzOp(ref *refBracket, prod *Bracket, o fuzzOp) (error, error) {
	switch o.name {
	case "R":
		return ref.report(o.r, o.i, o.w), prod.Report(o.r, o.i, o.w)
	case "C":
		return ref.correct(o.r, o.i, o.w), prod.Correct(o.r, o.i, o.w)
	default:
		return ref.withdraw(o.w), prod.Withdraw(o.w)
	}
}

// TestRandomDifferential2000：2000 组随机登记/更正/退赛序列与参考实现逐步对拍。
// 每个用例固定随机种子可重放；日志记录输入、被拒原因（判定依据）与每次状态变化。
func TestRandomDifferential2000(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewPCG(0xB7ACE7, 0x5EED5))
	for c := 0; c < cases; c++ {
		n := 2 + rng.IntN(63)
		ref := newRef(n)
		prod, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		steps := 4 + rng.IntN(45)
		var log strings.Builder
		fmt.Fprintf(&log, "case=%d N=%d steps=%d\n", c, n, steps)

		for step := 0; step < steps; step++ {
			o := fuzzOp{name: []string{"R", "C", "W"}[rng.IntN(3)]}
			switch o.name {
			case "W":
				o.w = 1 + rng.IntN(n)
			default:
				o.r = 1 + rng.IntN(ref.rounds)
				o.i = 1 + rng.IntN(ref.b>>uint(o.r))
				left, right, _ := ref.participant(o.r, o.i)
				switch rng.IntN(5) {
				case 0:
					o.w = left
				case 1:
					o.w = right
				case 2:
					o.w = 1 + rng.IntN(n) // 可能是非法参赛者
				case 3:
					o.w = n + 1 + rng.IntN(5)
				default:
					o.w = 0
				}
			}
			refErr, prodErr := applyFuzzOp(ref, prod, o)
			fpRef, fpProd := ref.fingerprint(), prod.fingerprint()
			outcome := "accepted"
			basis := "applied; cascade fixed-point reached"
			if refErr != nil {
				outcome = "rejected"
				basis = refErr.Error()
			}
			fmt.Fprintf(&log, "  step=%d %s -> %s [%s]\n", step, o, outcome, basis)
			if (refErr == nil) != (prodErr == nil) || fmt.Sprint(refErr) != fmt.Sprint(prodErr) {
				t.Fatalf("case=%d N=%d step=%d op=%s\nref err=%v\nprod err=%v\nLOG:\n%s",
					c, n, step, o, refErr, prodErr, log.String())
			}
			if fpRef != fpProd {
				afterRef, afterProd := refSnapshot(ref), prodSnapshot(prod)
				t.Fatalf("case=%d N=%d step=%d op=%s tree mismatch\nREF:\n%s\nPROD:\n%s\nLOG:\n%s",
					c, n, step, o, afterRef.rows, afterProd.rows, log.String())
			}
		}
		// 用例通过时仅打印一行；失败时由上面的 Fatalf 输出完整输入/输出/依据日志。
		t.Logf("case=%d N=%d ok (%d ops)", c, n, steps)
		assertInvariants(t, prod, n)
	}
}

func assertInvariants(t *testing.T, b *Bracket, n int) {
	t.Helper()
	champ := b.Champion()
	finalDecided := false
	pending := map[int]bool{} // 仍处于未登记场次（左右位均计入，用于“至多一个未登记场”）
	for _, m := range b.Bracket() {
		if m.Winner != 0 && m.Winner != m.Left && m.Winner != m.Right {
			t.Fatalf("winner %d not participant in %+v", m.Winner, m)
		}
		if m.Kind == KindTechnical {
			wdLeft := m.Left != 0 && b.withdrawn[m.Left]
			wdRight := m.Right != 0 && b.withdrawn[m.Right]
			if !wdLeft && !wdRight {
				t.Fatalf("technical result without withdrawn player: %+v", m)
			}
		}
		if m.Kind == KindBye {
			if m.Left != 0 && m.Right != 0 {
				t.Fatalf("bye match with two players: %+v", m)
			}
		}
		if m.Round == b.rounds && m.Index == 1 && m.Kind != KindOpen {
			finalDecided = true
			if m.Winner != champ {
				t.Fatalf("champion=%d but final winner=%d", champ, m.Winner)
			}
		}
		if m.Kind == KindOpen && m.Ready {
			if pending[m.Left] || pending[m.Right] {
				t.Fatalf("seed in multiple pending matches near %+v", m)
			}
			pending[m.Left] = true
			pending[m.Right] = true
		}
	}
	if finalDecided && champ == 0 {
		t.Fatal("final decided but Champion()=0")
	}
	if !finalDecided && champ != 0 {
		t.Fatal("champion exists before final result")
	}
}
