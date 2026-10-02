// Package seating 实现带保留时限与孤座回避的影厅选座登记器。
package seating

import (
	"errors"
	"sync"
)

// SeatState 表示某个座位在指定时刻视角下的状态。
type SeatState int

const (
	// SeatFree 座位空闲。
	SeatFree SeatState = iota
	// SeatHeld 座位被活跃保留占用。
	SeatHeld
	// SeatConfirmed 座位被已确认订单永久占用。
	SeatConfirmed
)

var (
	// ErrInvalidConfig 构造参数非法（R 不在 [1,26]、W 不在 [1,40] 或 T 小于 1）。
	ErrInvalidConfig = errors.New("seating: invalid config")
	// ErrClockRollback 时刻早于已被接受操作见过的最大时刻。
	ErrClockRollback = errors.New("seating: clock rollback")
	// ErrInvalidGroupSize 人数 k 不在 [1,8]。
	ErrInvalidGroupSize = errors.New("seating: invalid group size")
	// ErrNoSeats 没有任何排存在 k 个连续空闲座。
	ErrNoSeats = errors.New("seating: no consecutive seats")
	// ErrHoldNotFound 保留号从未发出。
	ErrHoldNotFound = errors.New("seating: hold not found")
	// ErrHoldConfirmed 保留已确认。
	ErrHoldConfirmed = errors.New("seating: hold already confirmed")
	// ErrHoldReleased 保留已释放。
	ErrHoldReleased = errors.New("seating: hold already released")
	// ErrHoldExpired 保留已过期。
	ErrHoldExpired = errors.New("seating: hold expired")
)

// HoldResult 是一次成功保留的结果。
type HoldResult struct {
	ID    int // 保留号，从 1 起严格自增，仅成功才消耗
	Row   int // 排号，1 起
	Start int // 起始座号，1 起
}

type holdStatus int

const (
	holdActive holdStatus = iota
	holdConfirmed
	holdReleased
)

type hold struct {
	row     int
	start   int
	size    int
	created int64
	expiry  int64
	status  holdStatus
}

// Registrar 是影厅选座登记器，所有方法可并发调用，
// 效果等价于某个串行顺序。
type Registrar struct {
	mu     sync.Mutex
	rows   int
	width  int
	ttl    int64
	holds  []*hold // holds[id-1] 即保留号 id 的记录
	maxNow int64
	seen   bool
}

// New 创建一个 R 排、每排 W 座、保留时限 T 的登记器。
// R 必须在 [1,26]，W 必须在 [1,40]，T 必须不小于 1，否则返回 ErrInvalidConfig。
func New(rows, width int, ttl int64) (*Registrar, error) {
	if rows < 1 || rows > 26 || width < 1 || width > 40 || ttl < 1 {
		return nil, ErrInvalidConfig
	}
	return &Registrar{rows: rows, width: width, ttl: ttl}, nil
}

// checkClock 校验调用方时钟，不推进已见最大时刻。
func (r *Registrar) checkClock(now int64) error {
	if r.seen && now < r.maxNow {
		return ErrClockRollback
	}
	return nil
}

// acceptClock 在操作被接受后推进已见最大时刻。
func (r *Registrar) acceptClock(now int64) {
	r.maxNow = now
	r.seen = true
}

// activeAt 报告保留在 now 时刻是否仍在占座。
// 过期口径：now >= expiry 即已过期不再占座。
func (h *hold) activeAt(now int64) bool {
	return h.status == holdActive && now < h.expiry
}

// freeAt 计算 now 时刻每个座位是否空闲：
// 没有已确认订单占用，也没有活跃保留占用。
func (r *Registrar) freeAt(now int64) [][]bool {
	free := make([][]bool, r.rows)
	for i := range free {
		row := make([]bool, r.width)
		for j := range row {
			row[j] = true
		}
		free[i] = row
	}
	for _, h := range r.holds {
		occupied := h.status == holdConfirmed || h.activeAt(now)
		if !occupied {
			continue
		}
		for seat := h.start; seat < h.start+h.size; seat++ {
			free[h.row-1][seat-1] = false
		}
	}
	return free
}

// candidate 是一个可放置 k 个连续空闲座的候选段。
type candidate struct {
	row       int
	start     int
	deviation int // 段中心偏离 |(2s+k-1)-(W+1)|
	orphan    bool
}

// candidates 枚举所有排中全部空闲的连续段 [s, s+k-1]，
// 并标注每个候选选定后是否会在任一侧留下孤座。
func (r *Registrar) candidates(free [][]bool, k int) []candidate {
	var out []candidate
	for row := 0; row < r.rows; row++ {
		seat := 0
		for seat < r.width {
			if !free[row][seat] {
				seat++
				continue
			}
			runStart := seat
			for seat < r.width && free[row][seat] {
				seat++
			}
			runEnd := seat // 空闲连续区为 [runStart, runEnd)，0 起
			for s := runStart; s+k <= runEnd; s++ {
				leftLen := s - runStart      // 左侧紧邻空闲区长度
				rightLen := runEnd - (s + k) // 右侧紧邻空闲区长度
				orphan := leftLen == 1 || rightLen == 1
				dev := abs(2*(s+1) + k - 1 - (r.width + 1))
				out = append(out, candidate{
					row:       row + 1,
					start:     s + 1,
					deviation: dev,
					orphan:    orphan,
				})
			}
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// less 实现选择次序：排号小者优先、段中心偏离小者优先、起始座号小者优先。
func less(a, b candidate) bool {
	if a.row != b.row {
		return a.row < b.row
	}
	if a.deviation != b.deviation {
		return a.deviation < b.deviation
	}
	return a.start < b.start
}

// Hold 为 k 人在同一排保留座号连续的 k 个空闲座。
// 先只在全部排的无孤座候选中选，若一个都没有才退到全部候选。
func (r *Registrar) Hold(k int, now int64) (HoldResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return HoldResult{}, err
	}
	if k < 1 || k > 8 {
		return HoldResult{}, ErrInvalidGroupSize
	}

	all := r.candidates(r.freeAt(now), k)
	pool := all[:0]
	for _, c := range all {
		if !c.orphan {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		pool = all // 第一遍无候选才退到第二遍
	}
	if len(pool) == 0 {
		return HoldResult{}, ErrNoSeats
	}

	best := pool[0]
	for _, c := range pool[1:] {
		if less(c, best) {
			best = c
		}
	}

	h := &hold{
		row:     best.row,
		start:   best.start,
		size:    k,
		created: now,
		expiry:  now + r.ttl,
		status:  holdActive,
	}
	r.acceptClock(now)
	r.holds = append(r.holds, h)
	return HoldResult{ID: len(r.holds), Row: best.row, Start: best.start}, nil
}

// settle 是 Confirm 与 Release 的公共部分：
// 依次按 未发出、已确认、已释放、已过期 判定，全部通过才把保留置为 target。
func (r *Registrar) settle(id int, now int64, target holdStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if id < 1 || id > len(r.holds) {
		return ErrHoldNotFound
	}
	h := r.holds[id-1]
	switch {
	case h.status == holdConfirmed:
		return ErrHoldConfirmed
	case h.status == holdReleased:
		return ErrHoldReleased
	case now >= h.expiry:
		return ErrHoldExpired
	}
	r.acceptClock(now)
	h.status = target
	return nil
}

// Confirm 把活跃保留转为永久占用。
func (r *Registrar) Confirm(id int, now int64) error {
	return r.settle(id, now, holdConfirmed)
}

// Release 把活跃保留释放，座位立即空闲。
func (r *Registrar) Release(id int, now int64) error {
	return r.settle(id, now, holdReleased)
}

// Seats 返回每个座位在 now 视角下的状态。
// 它不检查也不更新已见最大 now，查询不改变任何状态。
func (r *Registrar) Seats(now int64) [][]SeatState {
	r.mu.Lock()
	defer r.mu.Unlock()

	free := r.freeAt(now)
	out := make([][]SeatState, r.rows)
	for row := 0; row < r.rows; row++ {
		out[row] = make([]SeatState, r.width)
		for seat := 0; seat < r.width; seat++ {
			if !free[row][seat] {
				out[row][seat] = SeatHeld // 先按保留标记，下面再覆盖已确认
			}
		}
	}
	for _, h := range r.holds {
		if h.status != holdConfirmed {
			continue
		}
		for seat := h.start; seat < h.start+h.size; seat++ {
			out[h.row-1][seat-1] = SeatConfirmed
		}
	}
	return out
}
