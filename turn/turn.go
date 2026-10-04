package turn

import "ontology/input"

// Source 标识一条玩家记录中输入的来源。
type Source int

const (
	// Blank 是空填：无输入可重复，或连续缺失已超过重复上限 R。
	Blank Source = iota
	// Live 是实交：玩家在本回合提交窗口内真的交过输入。
	Live
	// Repeat 是重复：沿用该玩家最近一次实交输入。
	Repeat
)

// Slot 是一名玩家在一个已结算回合的记录。
type Slot struct {
	Cmd    []byte
	Source Source
}

// Record 是一个已结算回合的完整记录，恰含 N 条 Slot。
type Record struct {
	Turn  int
	TS    int64
	Slots []Slot
}

// Settler 是 turn 对 input 窗口的最小依赖面。
type Settler interface {
	// Take 取出并移除回合 k 的全部槽位。
	Take(k int) map[int]*input.Entry
	// Deactivate 把玩家 p 的已存槽位从就绪计数中剔除（槽位本身保留）。
	Deactivate(p int)
	// MarkActive 把玩家 p 的已存槽位重新计入就绪计数。
	MarkActive(p int)
}

type player struct {
	active bool
	miss   int
	last   []byte
}

// Engine 持有全部玩家的结算状态：活跃标记、连续缺失数、最近实交。
type Engine struct {
	n       int
	repeat  int
	dropAt  int
	players []player
	activeN int
	dropped []int
}

func New(n, repeatLimit, dropThreshold int) *Engine {
	e := &Engine{
		n:       n,
		repeat:  repeatLimit,
		dropAt:  dropThreshold,
		players: make([]player, n),
		activeN: n,
		dropped: make([]int, 0),
	}
	for p := range e.players {
		e.players[p].active = true
	}
	return e
}

func (e *Engine) ActiveCount() int {
	return e.activeN
}

func (e *Engine) Active(p int) bool {
	return e.players[p].active
}

// Miss 返回玩家当前连续缺失数（观测口）。
func (e *Engine) Miss(p int) int {
	return e.players[p].miss
}

// Reactivate 让玩家立即恢复活跃；m 不变，缺失数延后到其实交回合结算才清零。
func (e *Engine) Reactivate(p int) {
	if !e.players[p].active {
		e.players[p].active = true
		e.activeN++
	}
}

// Dropped 返回上一次 Settle 新掉线的玩家列表（由编排层用于同步窗口计数）。
func (e *Engine) Dropped() []int {
	return e.dropped
}

// Settle 在 ts 结算回合 k：对每个活跃/非活跃玩家产出一条 Slot，
// 更新 m、最近实交与活跃标记，并通过 w 同步提交窗口。
func (e *Engine) Settle(k int, ts int64, w Settler) *Record {
	e.dropped = e.dropped[:0]
	submitted := w.Take(k)
	rec := &Record{Turn: k, TS: ts, Slots: make([]Slot, e.n)}

	for p := 0; p < e.n; p++ {
		pl := &e.players[p]
		if ent, ok := submitted[p]; ok {
			cmd := make([]byte, len(ent.Cmd))
			copy(cmd, ent.Cmd)
			rec.Slots[p] = Slot{Cmd: cmd, Source: Live}
			pl.miss = 0
			pl.last = cmd
			continue
		}

		pl.miss++
		if pl.miss <= e.repeat && pl.last != nil {
			cmd := make([]byte, len(pl.last))
			copy(cmd, pl.last)
			rec.Slots[p] = Slot{Cmd: cmd, Source: Repeat}
		} else {
			rec.Slots[p] = Slot{Cmd: nil, Source: Blank}
		}

		// 活跃玩家在本回合结算后达到掉线阈值：置非活跃，并丢弃其窗口内
		// 已存的后续回合输入（这些输入此后不再参与到齐计数）。
		if pl.active && pl.miss >= e.dropAt {
			pl.active = false
			e.activeN--
			e.dropped = append(e.dropped, p)
			w.Deactivate(p)
		}
	}

	return rec
}
