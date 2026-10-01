// Package detector 实现支付卡不可能行程检测器。
//
// 每张卡维护：锚点（最近一次被接受交易的 t0、x0、y0）、拒绝历史
// （每次「不可能行程」拒绝的交易时刻）、冻结标志与差旅窗口。
// 所有方法可并发调用，效果等价于某个串行顺序；CheckBatch 是一个原子步骤。
package detector

import (
	"errors"
	"sort"
	"sync"
)

// 参数取值范围。
const (
	maxV           = 1_000_000          // 最高速度上限（公里/小时）
	maxK           = 100                // 冻结阈值上限
	maxH           = 1_000_000_000_000  // 拒绝窗口上限（秒）
	maxT           = 1_000_000_000_000  // 交易时刻上限（秒）
	maxCoord       = 1_000_000_000      // 坐标绝对值上限（公里）
	maxWindowBound = 10_000_000_000_000 // 差旅窗口端点上限（秒）
	maxBatch       = 1000               // 单批最大笔数
)

// Result 是 Check / CheckBatch 的判定结果。
type Result int

const (
	// Accepted 接受（含差旅免检接受）。
	Accepted Result = iota
	// RejectedInvalidParam 参数非法。
	RejectedInvalidParam
	// RejectedFrozen 卡已冻结。
	RejectedFrozen
	// RejectedDuplicate 与锚点三项完全相同的重复交易。
	RejectedDuplicate
	// RejectedOutOfOrder t 小于锚点时刻的乱序交易。
	RejectedOutOfOrder
	// RejectedImpossibleTravel 所需移动速度超过最高速度。
	RejectedImpossibleTravel
)

// String 返回判定结果的可读描述。
func (r Result) String() string {
	switch r {
	case Accepted:
		return "accepted"
	case RejectedInvalidParam:
		return "rejected: invalid parameter"
	case RejectedFrozen:
		return "rejected: card frozen"
	case RejectedDuplicate:
		return "rejected: duplicate"
	case RejectedOutOfOrder:
		return "rejected: out of order"
	case RejectedImpossibleTravel:
		return "rejected: impossible travel"
	default:
		return "unknown result"
	}
}

// 操作级错误（Declare / Unfreeze / CheckBatch 整体拒绝 / 构造失败）。
var (
	ErrInvalidParam  = errors.New("detector: invalid parameter")
	ErrCardNotFound  = errors.New("detector: card does not exist")
	ErrCardNotFrozen = errors.New("detector: card is not frozen")
)

// Txn 是一笔待检交易：t 为秒，x、y 为整数公里坐标。
type Txn struct {
	T int64
	X int64
	Y int64
}

// cardState 是单张卡的内部状态。
type cardState struct {
	hasAnchor  bool
	anchorT    int64
	anchorX    int64
	anchorY    int64
	rejections []int64 // 「不可能行程」拒绝时刻，只增不减，直到 Unfreeze
	frozen     bool
	hasWindow  bool
	windowFrom int64
	windowTo   int64
}

// Snapshot 是单张卡状态的只读快照，用于查询与测试核对。
type Snapshot struct {
	Exists     bool
	HasAnchor  bool
	AnchorT    int64
	AnchorX    int64
	AnchorY    int64
	Rejections []int64
	Frozen     bool
	HasWindow  bool
	WindowFrom int64
	WindowTo   int64
}

// Detector 是支付卡不可能行程检测器。零值不可用，请用 NewDetector 构造。
type Detector struct {
	mu    sync.Mutex
	v     int64
	k     int64
	h     int64
	cards map[string]*cardState
}

// NewDetector 构造检测器：v 为最高速度（公里/小时，1..10^6），
// k 为冻结阈值（1..100），h 为拒绝窗口（秒，1..10^12）。
// 参数越界返回 ErrInvalidParam。
func NewDetector(v, k, h int64) (*Detector, error) {
	if v < 1 || v > maxV || k < 1 || k > maxK || h < 1 || h > maxH {
		return nil, ErrInvalidParam
	}
	return &Detector{
		v:     v,
		k:     k,
		h:     h,
		cards: make(map[string]*cardState),
	}, nil
}

// validTxn 校验单笔交易的参数范围。
func validTxn(t, x, y int64) bool {
	if t < 0 || t > maxT {
		return false
	}
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return false
	}
	return true
}

// cardLocked 返回卡状态，不存在则创建。调用方须持有 d.mu。
func (d *Detector) cardLocked(card string) *cardState {
	st, ok := d.cards[card]
	if !ok {
		st = &cardState{}
		d.cards[card] = st
	}
	return st
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// checkLocked 对一笔参数合法的交易套用固定次序的判定与状态更新。
// 调用方须持有 d.mu，且 st 必须已存在。
func (d *Detector) checkLocked(st *cardState, t, x, y int64) Result {
	// 1. 已冻结。
	if st.frozen {
		return RejectedFrozen
	}
	// 2. 与锚点三项全部相同：重复。
	if st.hasAnchor && t == st.anchorT && x == st.anchorX && y == st.anchorY {
		return RejectedDuplicate
	}
	// 3. t 小于锚点时刻：乱序（t 相等但坐标不同不算）。
	if st.hasAnchor && t < st.anchorT {
		return RejectedOutOfOrder
	}
	// 4. 无锚点：接受并建立锚点。
	if !st.hasAnchor {
		st.setAnchor(t, x, y)
		return Accepted
	}
	// 5. t 落在差旅窗口 [from, to) 内：免检接受。
	if st.hasWindow && st.windowFrom <= t && t < st.windowTo {
		st.setAnchor(t, x, y)
		return Accepted
	}
	// 6. 速度判定：d*3600 <= V*dt 时接受（恰等接受）。
	dist := abs(x-st.anchorX) + abs(y-st.anchorY)
	dt := t - st.anchorT
	if dist*3600 <= d.v*dt {
		st.setAnchor(t, x, y)
		return Accepted
	}
	// 不可能行程：锚点不变，记入拒绝历史并按窗口计数，达到阈值即冻结
	// （本笔仍报不可能行程）。
	cnt := int64(1)
	expired := t - d.h // t_j 恰等于 t-H 视为已过期，不计入
	for _, tj := range st.rejections {
		if tj > expired {
			cnt++
		}
	}
	st.rejections = append(st.rejections, t)
	if cnt >= d.k {
		st.frozen = true
	}
	return RejectedImpossibleTravel
}

func (st *cardState) setAnchor(t, x, y int64) {
	st.hasAnchor = true
	st.anchorT = t
	st.anchorX = x
	st.anchorY = y
}

// Check 判定单笔交易。参数非法时返回 RejectedInvalidParam 且不改任何状态。
func (d *Detector) Check(card []byte, t, x, y int64) Result {
	if len(card) == 0 || !validTxn(t, x, y) {
		return RejectedInvalidParam
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checkLocked(d.cardLocked(string(card)), t, x, y)
}

// CheckBatch 原子地判定一批交易：先按 t 升序稳定排序（t 相同保持原下标
// 次序），再逐笔套用 Check 的判定与状态更新，返回按原下标次序排列的结果。
// 卡号为空、批大小不在 1..1000、或任一笔参数非法时整批拒绝，返回
// ErrInvalidParam 且不改任何状态。
func (d *Detector) CheckBatch(card []byte, txns []Txn) ([]Result, error) {
	if len(card) == 0 || len(txns) < 1 || len(txns) > maxBatch {
		return nil, ErrInvalidParam
	}
	for _, tx := range txns {
		if !validTxn(tx.T, tx.X, tx.Y) {
			return nil, ErrInvalidParam
		}
	}
	type indexedTxn struct {
		idx int
		txn Txn
	}
	order := make([]indexedTxn, len(txns))
	for i, tx := range txns {
		order[i] = indexedTxn{idx: i, txn: tx}
	}
	sort.SliceStable(order, func(a, b int) bool { return order[a].txn.T < order[b].txn.T })
	results := make([]Result, len(txns))
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.cardLocked(string(card))
	for _, it := range order {
		results[it.idx] = d.checkLocked(st, it.txn.T, it.txn.X, it.txn.Y)
	}
	return results, nil
}

// Declare 登记卡的差旅窗口 [from, to)（左闭右开），要求
// 0 <= from < to <= 10^13。每张卡只保留最后一次登记；对从未出现的卡
// 也可登记。参数非法返回 ErrInvalidParam 且不改任何状态。
func (d *Detector) Declare(card []byte, from, to int64) error {
	if len(card) == 0 || from < 0 || from >= to || to > maxWindowBound {
		return ErrInvalidParam
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.cardLocked(string(card))
	st.hasWindow = true
	st.windowFrom = from
	st.windowTo = to
	return nil
}

// Unfreeze 清除卡的冻结标志与全部拒绝历史，锚点与差旅窗口保持。
// 卡不存在（从未出现也未登记过）返回 ErrCardNotFound；卡未冻结返回
// ErrCardNotFrozen；两种错误按此顺序只报第一个。
func (d *Detector) Unfreeze(card []byte) error {
	if len(card) == 0 {
		return ErrInvalidParam
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.cards[string(card)]
	if !ok {
		return ErrCardNotFound
	}
	if !st.frozen {
		return ErrCardNotFrozen
	}
	st.frozen = false
	st.rejections = nil
	return nil
}

// Snapshot 返回卡状态的只读快照；卡不存在时返回零值（Exists 为 false）。
func (d *Detector) Snapshot(card []byte) Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.cards[string(card)]
	if !ok {
		return Snapshot{}
	}
	snap := Snapshot{
		Exists:     true,
		HasAnchor:  st.hasAnchor,
		AnchorT:    st.anchorT,
		AnchorX:    st.anchorX,
		AnchorY:    st.anchorY,
		Frozen:     st.frozen,
		HasWindow:  st.hasWindow,
		WindowFrom: st.windowFrom,
		WindowTo:   st.windowTo,
	}
	if len(st.rejections) > 0 {
		snap.Rejections = append([]int64(nil), st.rejections...)
	}
	return snap
}
