package reconcile

import (
	"errors"
	"sort"
	"sync"
)

// Side 标识流水来源侧：银行 Bank 或账簿 Book。
type Side int

const (
	// Bank 为银行流水侧。
	Bank Side = iota + 1
	// Book 为账簿流水侧。
	Book
)

// Line 为一条流水明细。
type Line struct {
	ID  string
	Amt int64
	Day int64
	Ref string
}

// Match 为一次成功配对。
type Match struct {
	Mid    int64
	Round  int
	BankID []string
	BookID []string
}

// 可区分的错误原因。
var (
	ErrInvalidSide   = errors.New("reconcile: invalid side")
	ErrInvalidLine   = errors.New("reconcile: invalid line")
	ErrDuplicateID   = errors.New("reconcile: duplicate id")
	ErrInvalidW      = errors.New("reconcile: invalid tolerance W")
	ErrMatchNotFound = errors.New("reconcile: match not found")
	ErrMatchReversed = errors.New("reconcile: match already reversed")
)

const (
	maxLines = 100000
	maxAmt   = 1_000_000_000_000
	maxDay   = 1_000_000_000
	maxW     = 1_000_000_000
)

type lineRec struct {
	line      Line
	matchedBy int64 // 0 表示未匹配，否则为所在有效匹配的 mid
}

type matchRec struct {
	mid   int64
	round int
	bank  []string
	book  []string
}

// Matcher 保存行、匹配、禁配集合与计数器。
// 所有方法在同一把互斥锁下串行化，Reconcile 的四轮是一个原子步骤。
type Matcher struct {
	mu       sync.Mutex
	bank     map[string]*lineRec
	book     map[string]*lineRec
	matches  map[int64]*matchRec
	reversed map[int64]bool
	forbid   map[string]map[string]struct{} // bankID -> set(bookID)
	nextMid  int64
}

// NewMatcher 创建空的对账匹配器。
func NewMatcher() *Matcher {
	return &Matcher{
		bank:     map[string]*lineRec{},
		book:     map[string]*lineRec{},
		matches:  map[int64]*matchRec{},
		reversed: map[int64]bool{},
		forbid:   map[string]map[string]struct{}{},
		nextMid:  1,
	}
}

// AddLine 登记一条明细行。错误按 侧非法 -> 行非法 -> 编号重复 的顺序只报第一个。
func (m *Matcher) AddLine(side Side, id string, amt, day int64, ref string) error {
	if side != Bank && side != Book {
		return ErrInvalidSide
	}
	if id == "" || amt == 0 || amt > maxAmt || amt < -maxAmt || day < 0 || day > maxDay {
		return ErrInvalidLine
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := m.bank
	if side == Book {
		lines = m.book
	}
	if len(lines) >= maxLines {
		return ErrInvalidLine
	}
	if _, ok := lines[id]; ok {
		return ErrDuplicateID
	}
	lines[id] = &lineRec{line: Line{ID: id, Amt: amt, Day: day, Ref: ref}}
	return nil
}

// Reconcile 原子执行四轮匹配，返回本次新增匹配（按产生次序）。
func (m *Matcher) Reconcile(w int64) ([]Match, error) {
	if w < 0 || w > maxW {
		return nil, ErrInvalidW
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var created []Match

	addMatch := func(round int, bankIDs, bookIDs []string) {
		sort.Strings(bankIDs)
		sort.Strings(bookIDs)
		rec := &matchRec{mid: m.nextMid, round: round, bank: bankIDs, book: bookIDs}
		m.nextMid++
		m.matches[rec.mid] = rec
		for _, id := range bankIDs {
			m.bank[id].matchedBy = rec.mid
		}
		for _, id := range bookIDs {
			m.book[id].matchedBy = rec.mid
		}
		created = append(created, Match{
			Mid:    rec.mid,
			Round:  round,
			BankID: append([]string(nil), bankIDs...),
			BookID: append([]string(nil), bookIDs...),
		})
	}

	bankIDs := unmatchedIDs(m.bank)
	bookIDs := unmatchedIDs(m.book)

	isForbidden := func(bankID, bookID string) bool {
		set, ok := m.forbid[bankID]
		if !ok {
			return false
		}
		_, bad := set[bookID]
		return bad
	}

	pickOne := func(b *lineRec, requireRef bool) string {
		bestID := ""
		bestDiff := int64(0)
		have := false
		for _, id := range bookIDs {
			k := m.book[id]
			if k.matchedBy != 0 {
				continue
			}
			if isForbidden(b.line.ID, id) {
				continue
			}
			if k.line.Amt != b.line.Amt {
				continue
			}
			if requireRef {
				if k.line.Ref == "" || k.line.Ref != b.line.Ref {
					continue
				}
			}
			diff := k.line.Day - b.line.Day
			if diff < 0 {
				diff = -diff
			}
			if diff > w {
				continue
			}
			if !have || diff < bestDiff || (diff == bestDiff && id < bestID) {
				have = true
				bestDiff = diff
				bestID = id
			}
		}
		return bestID
	}

	// 第一轮：ref 非空且相同 + 金额相等。
	for _, id := range bankIDs {
		b := m.bank[id]
		if b.matchedBy != 0 || b.line.Ref == "" {
			continue
		}
		if k := pickOne(b, true); k != "" {
			addMatch(1, []string{id}, []string{k})
		}
	}

	// 第二轮：不要求 ref，其余同第一轮。
	for _, id := range bankIDs {
		b := m.bank[id]
		if b.matchedBy != 0 {
			continue
		}
		if k := pickOne(b, false); k != "" {
			addMatch(2, []string{id}, []string{k})
		}
	}

	// 第三轮：一对多，仅处理 ref 非空的银行行。
	for _, id := range bankIDs {
		b := m.bank[id]
		if b.matchedBy != 0 || b.line.Ref == "" {
			continue
		}
		var s []string
		var sum int64
		for _, kid := range bookIDs {
			k := m.book[kid]
			if k.matchedBy != 0 || k.line.Ref != b.line.Ref {
				continue
			}
			diff := k.line.Day - b.line.Day
			if diff < 0 {
				diff = -diff
			}
			if diff > w || isForbidden(id, kid) {
				continue
			}
			s = append(s, kid)
			sum += k.line.Amt
		}
		if len(s) >= 2 && sum == b.line.Amt {
			addMatch(3, []string{id}, s)
		}
	}

	// 第四轮：多对一，仅处理 ref 非空的账簿行。
	for _, id := range bookIDs {
		k := m.book[id]
		if k.matchedBy != 0 || k.line.Ref == "" {
			continue
		}
		var t []string
		var sum int64
		for _, bid := range bankIDs {
			b := m.bank[bid]
			if b.matchedBy != 0 || b.line.Ref != k.line.Ref {
				continue
			}
			diff := b.line.Day - k.line.Day
			if diff < 0 {
				diff = -diff
			}
			if diff > w || isForbidden(bid, id) {
				continue
			}
			t = append(t, bid)
			sum += b.line.Amt
		}
		if len(t) >= 2 && sum == k.line.Amt {
			addMatch(4, t, []string{id})
		}
	}

	return created, nil
}

// Reverse 撤销仍有效的匹配：行回到未匹配，全部 (银行,账簿) 组合加入 F。
// 错误按 从未产生 -> 已撤销 的顺序只报第一个。返回按 id 升序恢复的银行行与账簿行。
func (m *Matcher) Reverse(mid int64) ([]Line, []Line, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.matches[mid]
	if !ok {
		if m.reversed[mid] {
			return nil, nil, ErrMatchReversed
		}
		return nil, nil, ErrMatchNotFound
	}
	delete(m.matches, mid)
	m.reversed[mid] = true

	bankLines := make([]Line, 0, len(rec.bank))
	bookLines := make([]Line, 0, len(rec.book))
	for _, bid := range rec.bank {
		b := m.bank[bid]
		b.matchedBy = 0
		bankLines = append(bankLines, b.line)
		set, ok := m.forbid[bid]
		if !ok {
			set = map[string]struct{}{}
			m.forbid[bid] = set
		}
		for _, kid := range rec.book {
			set[kid] = struct{}{}
		}
	}
	for _, kid := range rec.book {
		k := m.book[kid]
		k.matchedBy = 0
		bookLines = append(bookLines, k.line)
	}
	sort.Slice(bankLines, func(i, j int) bool { return bankLines[i].ID < bankLines[j].ID })
	sort.Slice(bookLines, func(i, j int) bool { return bookLines[i].ID < bookLines[j].ID })
	return bankLines, bookLines, nil
}

// Unmatched 按 id 字节序升序返回指定侧的未匹配行。
func (m *Matcher) Unmatched(side Side) ([]Line, error) {
	if side != Bank && side != Book {
		return nil, ErrInvalidSide
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := m.bank
	if side == Book {
		lines = m.book
	}
	var out []Line
	for _, rec := range lines {
		if rec.matchedBy == 0 {
			out = append(out, rec.line)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Matches 返回全部有效匹配，按银行行 id 列表中最小者升序、再按 mid 升序。
func (m *Matcher) Matches() []Match {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Match, 0, len(m.matches))
	for _, rec := range m.matches {
		out = append(out, Match{
			Mid:    rec.mid,
			Round:  rec.round,
			BankID: append([]string(nil), rec.bank...),
			BookID: append([]string(nil), rec.book...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BankID[0] != out[j].BankID[0] {
			return out[i].BankID[0] < out[j].BankID[0]
		}
		return out[i].Mid < out[j].Mid
	})
	return out
}

// Forbidden 返回禁配对 (银行 id, 账簿 id) 的快照，按二元组升序排列。
func (m *Matcher) Forbidden() [][2]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][2]string
	for bid, set := range m.forbid {
		for kid := range set {
			out = append(out, [2]string{bid, kid})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func unmatchedIDs(lines map[string]*lineRec) []string {
	var ids []string
	for id, rec := range lines {
		if rec.matchedBy == 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
