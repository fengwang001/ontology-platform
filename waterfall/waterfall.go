package waterfall

import (
	"errors"
	"sync"
)

// MaxTotal 是各层已收额之和允许达到的上限（含）。
const MaxTotal int64 = 1_000_000_000_000_000

// Layer 描述瀑布中的一层。
//
// 前 n-1 层为有上限层：Cap 为该层累计可收额（非负），Fee 被忽略。
// 最后一层为余额层：Cap 字段无意义（无上限），Fee 为管理人提成百分比（0-100）。
type Layer struct {
	// Cap 为有上限层（前 n-1 层）的累计上限，必须非负；余额层忽略该字段。
	Cap int64
	// Fee 仅对余额层（最后一层）有效，为管理人提成百分比，取值 0-100。
	Fee  int
	recv int64
}

// 可区分的拒绝原因。
var (
	ErrInvalidConfig       = errors.New("waterfall: config must contain at least two layers")
	ErrNegativeCap         = errors.New("waterfall: cap must be non-negative")
	ErrFeeOutOfRange       = errors.New("waterfall: fee percent must be in [0, 100]")
	ErrAmountNotPositive   = errors.New("waterfall: amount must be positive")
	ErrClawbackExceedsRecv = errors.New("waterfall: clawback amount exceeds total received")
	ErrTotalExceedsLimit   = errors.New("waterfall: total received would exceed 1e15")
)

// Waterfall 是带追回的分层瀑布分账器。零值不可用，须用 New 构造。
type Waterfall struct {
	mu     sync.RWMutex
	layers []Layer
	fee    int
	total  int64
}

// New 根据按顺序排列的层配置构造分账器并校验配置。
func New(layers []Layer) (*Waterfall, error) {
	if len(layers) < 2 {
		return nil, ErrInvalidConfig
	}
	for i := range layers[:len(layers)-1] {
		if layers[i].Cap < 0 {
			return nil, ErrNegativeCap
		}
	}
	fee := layers[len(layers)-1].Fee
	if fee < 0 || fee > 100 {
		return nil, ErrFeeOutOfRange
	}
	cp := make([]Layer, len(layers))
	copy(cp, layers)
	return &Waterfall{layers: cp, fee: fee}, nil
}

// Allocate 把正整数 x 从第 0 层起依次灌入各层上限，余额全部进入末层。
func (w *Waterfall) Allocate(x int64) error {
	if x <= 0 {
		return ErrAmountNotPositive
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.total > MaxTotal-x {
		return ErrTotalExceedsLimit
	}
	remaining := x
	for i := range w.layers[:len(w.layers)-1] {
		free := w.layers[i].Cap - w.layers[i].recv
		fill := remaining
		if fill > free {
			fill = free
		}
		w.layers[i].recv += fill
		remaining -= fill
		if remaining == 0 {
			break
		}
	}
	w.layers[len(w.layers)-1].recv += remaining
	w.total += x
	return nil
}

// Clawback 从末层起逆序追回 y，撤销此前分配的总额。
func (w *Waterfall) Clawback(y int64) error {
	if y <= 0 {
		return ErrAmountNotPositive
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if y > w.total {
		return ErrClawbackExceedsRecv
	}
	remaining := y
	for i := len(w.layers) - 1; i >= 0; i-- {
		cut := remaining
		if cut > w.layers[i].recv {
			cut = w.layers[i].recv
		}
		w.layers[i].recv -= cut
		remaining -= cut
		if remaining == 0 {
			break
		}
	}
	w.total -= y
	return nil
}

// Snapshot 返回各层已收额的副本（最后一个元素为余额层）。
func (w *Waterfall) Snapshot() []int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshotLocked()
}

func (w *Waterfall) snapshotLocked() []int64 {
	out := make([]int64, len(w.layers))
	for i := range w.layers {
		out[i] = w.layers[i].recv
	}
	return out
}

// Total 返回当前已分配总额（各层已收额之和）。
func (w *Waterfall) Total() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.total
}

// Split 返回余额层按累计已收额 R 计算的拆分：
// manager = floor(R*g/100)，investor = R - manager。
func (w *Waterfall) Split() (manager, investor int64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.splitLocked()
}

func (w *Waterfall) splitLocked() (manager, investor int64) {
	r := w.layers[len(w.layers)-1].recv
	manager = r * int64(w.fee) / 100
	return manager, r - manager
}

// State 在同一个读锁内返回一致快照：各层已收额、总额与余额层拆分。
// 当调用方需要让这几个值对应同一个线性化点时使用本方法，
// 而不要分别调用 Snapshot、Total 与 Split（它们各自原子，但之间可能穿插写操作）。
func (w *Waterfall) State() (recv []int64, total, manager, investor int64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	recv = w.snapshotLocked()
	manager, investor = w.splitLocked()
	return recv, w.total, manager, investor
}
