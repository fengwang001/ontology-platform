// Package impossibletravel 实现支付卡不可能行程检测器。
//
// 检测器按卡维护最近一次被接受交易的锚点 (t0, x0, y0)、不可能行程
// 拒绝历史、冻结标志与差旅豁免窗口，按固定次序判定每笔交易，并以
// 滑动时间窗内的拒绝次数触发冻结。所有方法可并发调用，效果等价于
// 某个串行顺序；CheckBatch 是一个原子步骤。
package impossibletravel

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 参数取值范围。
const (
	MinSpeed       = 1
	MaxSpeed       = 1_000_000
	MinThreshold   = 1
	MaxThreshold   = 100
	MinWindow      = 1
	MaxWindow      = 1_000_000_000_000
	MinTime        = 0
	MaxTime        = 1_000_000_000_000
	MaxCoord       = 1_000_000_000
	MaxDeclareTime = 10_000_000_000_000
	MinBatchSize   = 1
	MaxBatchSize   = 1000
)

// Decision 是 Check / CheckBatch 对单笔交易的判定结果。
type Decision int

const (
	// Accepted 接受（含差旅免检接受）。
	Accepted Decision = iota
	// RejectedInvalidParam 参数非法（卡号为空、t 或坐标越界）。
	RejectedInvalidParam
	// RejectedFrozen 卡已冻结。
	RejectedFrozen
	// RejectedDuplicate 与锚点三项全部相同的重复交易。
	RejectedDuplicate
	// RejectedOutOfOrder t 小于锚点时刻的乱序交易。
	RejectedOutOfOrder
	// RejectedImpossibleTravel 所需移动速度超过最高速度。
	RejectedImpossibleTravel
)

func (d Decision) String() string {
	switch d {
	case Accepted:
		return "Accepted"
	case RejectedInvalidParam:
		return "RejectedInvalidParam"
	case RejectedFrozen:
		return "RejectedFrozen"
	case RejectedDuplicate:
		return "RejectedDuplicate"
	case RejectedOutOfOrder:
		return "RejectedOutOfOrder"
	case RejectedImpossibleTravel:
		return "RejectedImpossibleTravel"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// 操作级错误。
var (
	// ErrInvalidParam 构造参数、Declare 参数或整批参数非法。
	ErrInvalidParam = errors.New("impossibletravel: invalid parameter")
	// ErrCardNotFound Unfreeze 的卡从未出现也未登记过。
	ErrCardNotFound = errors.New("impossibletravel: card not found")
	// ErrCardNotFrozen Unfreeze 的卡未处于冻结状态。
	ErrCardNotFrozen = errors.New("impossibletravel: card not frozen")
)

// Txn 是一笔待检交易。
type Txn struct {
	T int64 // 秒，[0, 1e12]
	X int64 // 公里坐标，|x| <= 1e9
	Y int64 // 公里坐标，|y| <= 1e9
}

// CardState 是单卡状态的只读快照。
type CardState struct {
	HasAnchor  bool
	AnchorT    int64
	AnchorX    int64
	AnchorY    int64
	Rejections []int64 // 不可能行程拒绝历史（只增，Unfreeze 清空）
	Frozen     bool
	HasTravel  bool
	TravelFrom int64
	TravelTo   int64
}

type cardState struct {
	hasAnchor  bool
	t0, x0, y0 int64
	rejections []int64
	frozen     bool
	hasTravel  bool
	from, to   int64
}

// Detector 是支付卡不可能行程检测器。
type Detector struct {
	mu sync.Mutex
	v  int64 // 最高速度，公里/小时
	k  int   // 冻结阈值
	h  int64 // 拒绝窗口，秒

	cards map[string]*cardState
}

// NewDetector 构造检测器。V 为最高速度（公里/小时，1 到 1e6），
// K 为冻结阈值（1 到 100），H 为拒绝窗口（秒，1 到 1e12）。
// 参数越界返回 ErrInvalidParam。
func NewDetector(v, k, h int64) (*Detector, error) {
	if v < MinSpeed || v > MaxSpeed ||
		k < MinThreshold || k > MaxThreshold ||
		h < MinWindow || h > MaxWindow {
		return nil, ErrInvalidParam
	}
	return &Detector{
		v:     v,
		k:     int(k),
		h:     h,
		cards: make(map[string]*cardState),
	}, nil
}

func validTxn(t, x, y int64) bool {
	return t >= MinTime && t <= MaxTime &&
		x >= -MaxCoord && x <= MaxCoord &&
		y >= -MaxCoord && y <= MaxCoord
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Check 判定单笔交易，返回判定结果。判定按固定次序进行，
// 只报告第一个命中的拒绝原因：参数非法、卡已冻结、重复、
// 乱序、不可能行程。
func (d *Detector) Check(card []byte, t, x, y int64) Decision {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checkLocked(card, t, x, y)
}

func (d *Detector) checkLocked(card []byte, t, x, y int64) Decision {
	if len(card) == 0 || !validTxn(t, x, y) {
		return RejectedInvalidParam
	}
	cs, ok := d.cards[string(card)]
	if !ok {
		cs = &cardState{}
		d.cards[string(card)] = cs
	}
	if cs.frozen {
		return RejectedFrozen
	}
	if cs.hasAnchor {
		if t == cs.t0 && x == cs.x0 && y == cs.y0 {
			return RejectedDuplicate
		}
		if t < cs.t0 {
			return RejectedOutOfOrder
		}
	} else {
		cs.hasAnchor = true
		cs.t0, cs.x0, cs.y0 = t, x, y
		return Accepted
	}
	if cs.hasTravel && cs.from <= t && t < cs.to {
		cs.t0, cs.x0, cs.y0 = t, x, y
		return Accepted
	}
	dist := abs(x-cs.x0) + abs(y-cs.y0)
	dt := t - cs.t0
	if dist*3600 <= d.v*dt {
		cs.t0, cs.x0, cs.y0 = t, x, y
		return Accepted
	}
	cnt := 1
	for _, tj := range cs.rejections {
		if tj > t-d.h {
			cnt++
		}
	}
	cs.rejections = append(cs.rejections, t)
	if cnt >= d.k {
		cs.frozen = true
	}
	return RejectedImpossibleTravel
}

// CheckBatch 原子地判定一批交易：先按 t 升序稳定排序（t 相同
// 保持原下标次序），再逐笔套用 Check 的判定与状态更新，返回按
// 原下标次序排列的各笔结果。批内任一笔参数非法或批大小越界则
// 整批拒绝，不改变任何状态。
func (d *Detector) CheckBatch(card []byte, txns []Txn) ([]Decision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(card) == 0 || len(txns) < MinBatchSize || len(txns) > MaxBatchSize {
		return nil, ErrInvalidParam
	}
	for _, txn := range txns {
		if !validTxn(txn.T, txn.X, txn.Y) {
			return nil, ErrInvalidParam
		}
	}
	order := make([]int, len(txns))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return txns[order[a]].T < txns[order[b]].T
	})
	results := make([]Decision, len(txns))
	for _, idx := range order {
		results[idx] = d.checkLocked(card, txns[idx].T, txns[idx].X, txns[idx].Y)
	}
	return results, nil
}

// Declare 为卡登记差旅窗口 [from, to)（左闭右开），每张卡只保留
// 最后一次登记，对从未出现的卡也可登记。要求 0 <= from < to <= 1e13，
// 否则返回 ErrInvalidParam 且不改变任何状态。
func (d *Detector) Declare(card []byte, from, to int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(card) == 0 || from < 0 || from >= to || to > MaxDeclareTime {
		return ErrInvalidParam
	}
	cs, ok := d.cards[string(card)]
	if !ok {
		cs = &cardState{}
		d.cards[string(card)] = cs
	}
	cs.hasTravel = true
	cs.from, cs.to = from, to
	return nil
}

// Unfreeze 清除卡的冻结标志与全部拒绝历史，锚点与差旅窗口保持。
// 卡不存在（从未出现也未登记过）返回 ErrCardNotFound；卡未冻结
// 返回 ErrCardNotFrozen。按此顺序只报第一个错误。
func (d *Detector) Unfreeze(card []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cs, ok := d.cards[string(card)]
	if !ok {
		return ErrCardNotFound
	}
	if !cs.frozen {
		return ErrCardNotFrozen
	}
	cs.frozen = false
	cs.rejections = nil
	return nil
}

// State 返回卡状态的只读快照；卡不存在时 ok 为 false。
func (d *Detector) State(card []byte) (CardState, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cs, ok := d.cards[string(card)]
	if !ok {
		return CardState{}, false
	}
	return CardState{
		HasAnchor:  cs.hasAnchor,
		AnchorT:    cs.t0,
		AnchorX:    cs.x0,
		AnchorY:    cs.y0,
		Rejections: append([]int64(nil), cs.rejections...),
		Frozen:     cs.frozen,
		HasTravel:  cs.hasTravel,
		TravelFrom: cs.from,
		TravelTo:   cs.to,
	}, true
}
