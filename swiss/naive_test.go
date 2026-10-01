package swiss

import "errors"

// refModel 是按题目规则逐步直写的朴素参考实现：
// 每一步都严格照规则重算，用于与 Tournament 差分对照。
type refModel struct {
	r       int
	names   []string
	score   []int
	hadBye  []bool
	played  [][]bool
	opps    [][]int
	started bool
	rounds  int

	inRound bool
	pairs   [][2]int
	bye     int
	done    []bool
}

func newRefModel(r int) *refModel {
	return &refModel{r: r}
}

func (m *refModel) reg(name string) (int, error) {
	if m.started {
		return 0, ErrStarted
	}
	if name == "" {
		return 0, ErrEmptyName
	}
	for _, n := range m.names {
		if n == name {
			return 0, ErrDuplicate
		}
	}
	m.names = append(m.names, name)
	m.score = append(m.score, 0)
	m.hadBye = append(m.hadBye, false)
	m.opps = append(m.opps, nil)
	for i := range m.played {
		m.played[i] = append(m.played[i], false)
	}
	m.played = append(m.played, make([]bool, len(m.names)))
	return len(m.names), nil
}

func (m *refModel) pair() ([][2]int, int, error) {
	if m.inRound {
		return nil, 0, ErrPending
	}
	if m.rounds >= m.r {
		return nil, 0, ErrMaxRounds
	}
	n := len(m.names)
	if n < 2 {
		return nil, 0, ErrTooFew
	}

	var seq []int
	bye := -1
	if n%2 == 1 {
		for i := 0; i < n; i++ {
			if m.hadBye[i] {
				continue
			}
			if bye == -1 || m.score[i] < m.score[bye] ||
				(m.score[i] == m.score[bye] && i > bye) {
				bye = i
			}
		}
		if bye == -1 {
			return nil, 0, ErrNoPairing
		}
		for i := 0; i < n; i++ {
			if i != bye {
				seq = append(seq, i)
			}
		}
	} else {
		for i := 0; i < n; i++ {
			seq = append(seq, i)
		}
	}

	// 插入排序：积分降序，种子号升序。
	for i := 1; i < len(seq); i++ {
		for j := i; j > 0; j-- {
			a, b := seq[j-1], seq[j]
			if m.score[a] < m.score[b] ||
				(m.score[a] == m.score[b] && a > b) {
				seq[j-1], seq[j] = seq[j], seq[j-1]
			} else {
				break
			}
		}
	}

	used := make([]bool, len(seq))
	var out [][2]int
	for i := 0; i < len(seq); i++ {
		if used[i] {
			continue
		}
		j := -1
		for k := i + 1; k < len(seq); k++ {
			if !used[k] && !m.played[seq[i]][seq[k]] {
				j = k
				break
			}
		}
		if j == -1 {
			return nil, 0, ErrNoPairing
		}
		used[i], used[j] = true, true
		out = append(out, [2]int{seq[i] + 1, seq[j] + 1})
	}

	m.started = true
	m.rounds++
	m.inRound = true
	m.pairs = out
	m.done = make([]bool, len(out))
	if bye >= 0 {
		m.bye = bye + 1
		m.hadBye[bye] = true
		m.score[bye] += 2
	} else {
		m.bye = 0
	}
	res := make([][2]int, len(out))
	copy(res, out)
	return res, m.bye, nil
}

func (m *refModel) report(a, b, r int) error {
	if !m.inRound {
		return ErrNoRound
	}
	if r != 0 && r != 1 && r != 2 {
		return ErrBadResult
	}
	idx := -1
	for i, p := range m.pairs {
		if (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrUnknownPair
	}
	if m.done[idx] {
		return ErrAlreadyScore
	}
	m.done[idx] = true
	// r 始终从调用方 a 的角度计分；a、b 可按对阵任一方向传入。
	m.score[a-1] += r
	m.score[b-1] += 2 - r
	m.played[a-1][b-1] = true
	m.played[b-1][a-1] = true
	m.opps[a-1] = append(m.opps[a-1], b)
	m.opps[b-1] = append(m.opps[b-1], a)
	for _, d := range m.done {
		if !d {
			return nil
		}
	}
	m.inRound = false
	return nil
}

func (m *refModel) standings() []Row {
	type tmp struct {
		idx, score, buch int
	}
	var ts []tmp
	for i := range m.names {
		buch := 0
		for _, o := range m.opps[i] {
			buch += m.score[o-1]
		}
		ts = append(ts, tmp{i, m.score[i], buch})
	}
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0; j-- {
			a, b := ts[j-1], ts[j]
			less := b.score > a.score ||
				(b.score == a.score && b.buch > a.buch) ||
				(b.score == a.score && b.buch == a.buch && b.idx < a.idx)
			if less {
				ts[j-1], ts[j] = ts[j], ts[j-1]
			} else {
				break
			}
		}
	}
	rows := make([]Row, len(ts))
	for i, x := range ts {
		rows[i] = Row{Seed: x.idx + 1, Score: x.score, Buch: x.buch}
	}
	return rows
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func countGamesByes(m *refModel) (games, byes int) {
	for i := range m.names {
		games += len(m.opps[i])
		if m.hadBye[i] {
			byes++
		}
	}
	return games / 2, byes
}
