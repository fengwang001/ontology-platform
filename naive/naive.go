// Package naive 是看板规则的独立朴素参考实现。
//
// 与主实现 ontology/kanban 的区别（刻意为之，避免同源 bug）：
//   - 不维护任何增量计数器：每次容量判定都全量扫描全部卡片重算占用；
//   - 不用单把锁（对照模型在单线程测试中串行使用）；
//   - 拒绝判定顺序、错误码与规则语义严格照规格文档逐条重写。
package naive

import (
	"fmt"
	"sort"
	"strings"
)

type Code string

const (
	CodeInvalid Code = "invalid_argument"
	CodeClock   Code = "clock_rollback"
	CodeNoCard  Code = "card_not_found"
	CodeVersion Code = "version_conflict"
	CodeFlow    Code = "illegal_flow"
	CodeDep     Code = "dependency"
	CodeExp     Code = "expedite_busy"
	CodeColFull Code = "column_full"
	CodeOwnFull Code = "owner_full"
	CodeOK      Code = ""
)

type Outcome struct {
	Accepted bool
	Code     Code
	CardID   string
	Owner    string
	Col      int
	Version  int
	Exp      bool
	Prereqs  []string
}

type Card struct {
	id      string
	owner   string
	col     int
	ver     int
	exp     bool
	prereqs map[string]bool
}

type Model struct {
	ncols int
	lim   []int
	g     int
	cards map[string]*Card
	succ  map[string]map[string]bool
	last  int64
}

func New(cols []string, limits []int, g int) (*Model, error) {
	if len(cols) < 3 || len(cols) > 8 || len(cols) != len(limits) || g < 1 || g > 50 {
		return nil, fmt.Errorf("bad config")
	}
	for i := range cols {
		if cols[i] == "" || limits[i] < 0 {
			return nil, fmt.Errorf("bad config")
		}
		if (i == 0 || i == len(cols)-1) && limits[i] != 0 {
			return nil, fmt.Errorf("edge columns unlimited")
		}
	}
	return &Model{ncols: len(cols), lim: append([]int(nil), limits...), g: g,
		cards: map[string]*Card{}, succ: map[string]map[string]bool{}}, nil
}

func (m *Model) reject(c Code) Outcome { return Outcome{Accepted: false, Code: c} }

func (m *Model) clock(now int64) Code {
	if now < 0 || now > 1e12 {
		return CodeInvalid
	}
	if now < m.last {
		return CodeClock
	}
	return CodeOK
}

func (m *Model) AddCard(id, owner string, now int64) Outcome {
	if id == "" || owner == "" {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	if _, ok := m.cards[id]; ok {
		return m.reject(CodeInvalid)
	}
	m.cards[id] = &Card{id: id, owner: owner, col: 0, ver: 1, prereqs: map[string]bool{}}
	m.last = now
	return m.snapshot(id, true)
}

func (m *Model) SetLimit(col, limit int, now int64) Outcome {
	if col <= 0 || col >= m.ncols-1 || limit < 0 {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	m.lim[col] = limit
	m.last = now
	return Outcome{Accepted: true}
}

func inProg(c, n int) bool { return c > 0 && c < n-1 }

func (m *Model) Move(cardID string, to, expect int, exp bool, now int64) Outcome {
	if to < 0 || to >= m.ncols {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	c, ok := m.cards[cardID]
	if !ok {
		return m.reject(CodeNoCard)
	}
	if expect != c.ver {
		return m.reject(CodeVersion)
	}
	from := c.col
	last := m.ncols - 1
	switch {
	case from == last:
		return m.reject(CodeFlow)
	case to == from:
		return m.reject(CodeFlow)
	case to > from && to != from+1:
		return m.reject(CodeFlow)
	}
	if exp && !inProg(to, m.ncols) {
		return m.reject(CodeInvalid)
	}
	if from == 0 && to != 0 {
		for p := range c.prereqs {
			pc := m.cards[p]
			if pc == nil || pc.col != last {
				return m.reject(CodeDep)
			}
		}
	}
	grant := false
	if exp {
		switch {
		case c.exp:
			grant = true
		default:
			for _, other := range m.cards {
				if other.id != c.id && other.exp {
					return m.reject(CodeExp)
				}
			}
			grant = true
		}
	}
	// 全量重算：把 c 放到 to 后逐列/逐人统计。
	colCnt := make([]int, m.ncols)
	ownCnt := map[string]int{}
	for id, x := range m.cards {
		pos := x.col
		if id == cardID {
			pos = to
		}
		colCnt[pos]++
		if inProg(pos, m.ncols) {
			ownCnt[x.owner]++
		}
	}
	if !grant && inProg(to, m.ncols) {
		if m.lim[to] != 0 && colCnt[to] > m.lim[to] {
			return m.reject(CodeColFull)
		}
		if ownCnt[c.owner] > m.g {
			return m.reject(CodeOwnFull)
		}
	}
	c.col = to
	c.ver++
	if grant {
		c.exp = true
	}
	if !inProg(to, m.ncols) {
		c.exp = false
	}
	m.last = now
	return m.snapshot(cardID, true)
}

func (m *Model) Reopen(cardID string, expect int, now int64) Outcome {
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	c, ok := m.cards[cardID]
	if !ok {
		return m.reject(CodeNoCard)
	}
	if expect != c.ver {
		return m.reject(CodeVersion)
	}
	if c.col != m.ncols-1 {
		return m.reject(CodeFlow)
	}
	for s := range m.succ[cardID] {
		sc := m.cards[s]
		if sc != nil && sc.col != 0 {
			return m.reject(CodeDep)
		}
	}
	target := m.ncols - 2
	colCnt := 0
	for id, x := range m.cards {
		pos := x.col
		if id == cardID {
			pos = target
		}
		if pos == target {
			colCnt++
		}
	}
	if m.lim[target] != 0 && colCnt > m.lim[target] {
		return m.reject(CodeColFull)
	}
	own := 0
	for id, x := range m.cards {
		pos := x.col
		if id == cardID {
			pos = target
		}
		if inProg(pos, m.ncols) && x.owner == c.owner {
			own++
		}
	}
	if own > m.g {
		return m.reject(CodeOwnFull)
	}
	c.col = target
	c.ver++
	m.last = now
	return m.snapshot(cardID, true)
}

func (m *Model) ChangeOwner(cardID, newOwner string, expect int, now int64) Outcome {
	if newOwner == "" {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	c, ok := m.cards[cardID]
	if !ok {
		return m.reject(CodeNoCard)
	}
	if expect != c.ver {
		return m.reject(CodeVersion)
	}
	if newOwner == c.owner {
		return m.reject(CodeFlow)
	}
	if inProg(c.col, m.ncols) {
		cnt := 0
		for _, x := range m.cards {
			if x.id != cardID && inProg(x.col, m.ncols) && x.owner == newOwner {
				cnt++
			}
		}
		if cnt+1 > m.g {
			return m.reject(CodeOwnFull)
		}
	}
	c.owner = newOwner
	c.ver++
	m.last = now
	return m.snapshot(cardID, true)
}

func (m *Model) reaches(from, target string) bool {
	seen := map[string]bool{from: true}
	st := []string{from}
	for len(st) > 0 {
		n := st[len(st)-1]
		st = st[:len(st)-1]
		for s := range m.succ[n] {
			if s == target {
				return true
			}
			if !seen[s] {
				seen[s] = true
				st = append(st, s)
			}
		}
	}
	return false
}

func (m *Model) AddDep(cardID, pre string, expect int, now int64) Outcome {
	if cardID == "" || pre == "" {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	c, ok := m.cards[cardID]
	if !ok {
		return m.reject(CodeNoCard)
	}
	if expect != c.ver {
		return m.reject(CodeVersion)
	}
	pc, pok := m.cards[pre]
	if !pok {
		return m.reject(CodeNoCard)
	}
	if cardID == pre {
		return m.reject(CodeDep)
	}
	if c.prereqs[pre] {
		return m.reject(CodeDep)
	}
	if c.col != 0 && pc.col != m.ncols-1 {
		return m.reject(CodeDep)
	}
	if m.reaches(cardID, pre) {
		return m.reject(CodeDep)
	}
	c.prereqs[pre] = true
	if m.succ[pre] == nil {
		m.succ[pre] = map[string]bool{}
	}
	m.succ[pre][cardID] = true
	c.ver++
	m.last = now
	return Outcome{Accepted: true}
}

func (m *Model) RemoveDep(cardID, pre string, expect int, now int64) Outcome {
	if cardID == "" || pre == "" {
		return m.reject(CodeInvalid)
	}
	if c := m.clock(now); c != CodeOK {
		return m.reject(c)
	}
	c, ok := m.cards[cardID]
	if !ok {
		return m.reject(CodeNoCard)
	}
	if expect != c.ver {
		return m.reject(CodeVersion)
	}
	if _, pok := m.cards[pre]; !pok {
		return m.reject(CodeNoCard)
	}
	if !c.prereqs[pre] {
		return m.reject(CodeDep)
	}
	delete(c.prereqs, pre)
	delete(m.succ[pre], cardID)
	c.ver++
	m.last = now
	return Outcome{Accepted: true}
}

func (m *Model) snapshot(id string, accepted bool) Outcome {
	c := m.cards[id]
	ps := make([]string, 0, len(c.prereqs))
	for p := range c.prereqs {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return Outcome{Accepted: accepted, Code: CodeOK, CardID: id, Owner: c.owner,
		Col: c.col, Version: c.ver, Exp: c.exp, Prereqs: ps}
}

// DumpState 导出与主实现 fullState 同构的全量状态字符串。
func (m *Model) DumpState() string {
	parts := make([]string, 0, len(m.cards))
	for _, c := range m.cards {
		ps := make([]string, 0, len(c.prereqs))
		for p := range c.prereqs {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		e := 0
		if c.exp {
			e = 1
		}
		parts = append(parts, fmt.Sprintf("%s|%s|c%d|v%d|e%d|%s", c.id, c.owner, c.col, c.ver, e, strings.Join(ps, ",")))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}
