package kanban

import "sort"

type card struct {
	id        string
	owner     string
	col       int
	version   int
	expedited bool
	prereqs   map[string]struct{}
}

type column struct {
	wipLimit int // LimitUnlimited 表示不限
}

// Board 是单一看板的全部可变状态。并发访问由 Service 层互斥保护。
type Board struct {
	ownerLimit int
	columns    []column
	cards      map[string]*card
	lastNow    int64
	// successors 维护 prerequisite -> 后继集合，仅 AddDep/RemoveDep/Reopen 使用。
	successors map[string]map[string]struct{}
	// colCount[col] 为列内卡片数；ownerCount[owner] 为该负责人在进行中区域的卡片数。
	colCount   []int
	ownerCount map[string]int
	// expeditedCard 为当前持有加急标记的进行中卡片 ID，空串表示无。
	expeditedCard string
}

// Config 描述看板创建参数。
type Config struct {
	// Columns 为有序列清单：3 到 8 列。第一列待办，最后一列完成。
	// Limits 长度须等于 Columns；待办列与完成列的限制必须为 LimitUnlimited。
	Columns []string
	Limits  []int
	// OwnerLimit 是每个负责人在所有进行中阶段合计的在制品上限 G（1..50）。
	OwnerLimit int
}

const maxNow = int64(1_000_000_000_000)

// NewBoard 创建并校验看板。
func NewBoard(cfg Config) (*Board, error) {
	n := len(cfg.Columns)
	if n < 3 || n > 8 {
		return nil, newError(ErrInvalidArgument, "column count %d out of range [3,8]", n)
	}
	if len(cfg.Limits) != n {
		return nil, newError(ErrInvalidArgument, "limits length %d != columns %d", len(cfg.Limits), n)
	}
	for i, name := range cfg.Columns {
		if name == "" {
			return nil, newError(ErrInvalidArgument, "column %d has empty name", i)
		}
	}
	for i, lim := range cfg.Limits {
		switch {
		case lim < 0:
			return nil, newError(ErrInvalidArgument, "column %d limit negative", i)
		case i == 0 && lim != LimitUnlimited:
			return nil, newError(ErrInvalidArgument, "todo column must be unlimited")
		case i == n-1 && lim != LimitUnlimited:
			return nil, newError(ErrInvalidArgument, "done column must be unlimited")
		case i > 0 && i < n-1 && lim != LimitUnlimited && lim < 1:
			return nil, newError(ErrInvalidArgument, "wip limit must be positive or unlimited")
		}
	}
	if cfg.OwnerLimit < 1 || cfg.OwnerLimit > 50 {
		return nil, newError(ErrInvalidArgument, "owner limit G=%d out of [1,50]", cfg.OwnerLimit)
	}
	cols := make([]column, n)
	for i, lim := range cfg.Limits {
		cols[i] = column{wipLimit: lim}
	}
	return &Board{
		ownerLimit: cfg.OwnerLimit,
		columns:    cols,
		cards:      map[string]*card{},
		successors: map[string]map[string]struct{}{},
		colCount:   make([]int, n),
		ownerCount: map[string]int{},
	}, nil
}

// AddCard 在待办列创建卡片，版本号为 1。
func (b *Board) AddCard(id, owner string, now int64) (*Card, error) {
	if id == "" {
		return nil, newError(ErrInvalidArgument, "card id is empty")
	}
	if owner == "" {
		return nil, newError(ErrInvalidArgument, "owner is empty")
	}
	if now < 0 || now > maxNow {
		return nil, newError(ErrInvalidArgument, "now %d out of [0,1e12]", now)
	}
	if now < b.lastNow {
		return nil, newError(ErrClockRollback, "now %d < last accepted now %d", now, b.lastNow)
	}
	if _, exists := b.cards[id]; exists {
		return nil, newError(ErrInvalidArgument, "card %q already exists", id)
	}
	c := &card{id: id, owner: owner, col: 0, version: 1, prereqs: map[string]struct{}{}}
	b.cards[id] = c
	b.colCount[0]++
	b.lastNow = now
	return b.snapshot(c), nil
}

// SetColumnLimit 调整某进行中列的上限；不驱逐已有卡片，之后移入一律以新上限判定。
func (b *Board) SetColumnLimit(col, limit int, now int64) error {
	if col <= 0 || col >= len(b.columns)-1 {
		return newError(ErrInvalidArgument, "column %d is not an in-progress column", col)
	}
	if limit != LimitUnlimited && limit < 1 {
		return newError(ErrInvalidArgument, "wip limit must be positive or unlimited")
	}
	if now < 0 || now > maxNow {
		return newError(ErrInvalidArgument, "now %d out of [0,1e12]", now)
	}
	if now < b.lastNow {
		return newError(ErrClockRollback, "now %d < last accepted now %d", now, b.lastNow)
	}
	b.columns[col].wipLimit = limit
	b.lastNow = now
	return nil
}

// snapshot 返回不可变卡片快照。
func (b *Board) snapshot(c *card) *Card {
	prereqs := make([]string, 0, len(c.prereqs))
	for p := range c.prereqs {
		prereqs = append(prereqs, p)
	}
	sort.Strings(prereqs)
	return &Card{
		ID:        c.id,
		Owner:     c.owner,
		Column:    c.col,
		Version:   c.version,
		Expedited: c.expedited,
		Prereqs:   prereqs,
	}
}

func (b *Board) isInProgress(col int) bool { return col > 0 && col < len(b.columns)-1 }
