package history

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// 共享哨兵错误：参数非法、时钟回退、文档不存在。
// lease 包的租约类错误在其包内单独定义，errors.Is 均可区分。
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockBack       = errors.New("clock moved backwards")
	ErrDocNotFound     = errors.New("document not found")
)

const (
	maxNow = int64(1_000_000_000_000)
	minID  = 1
	maxID  = 256
)

type Kind int

const (
	KindIndex Kind = iota + 1
	KindDelete
)

type Op struct {
	Seq  int64
	ID   []byte
	Kind Kind
}

type MergeResult struct {
	Cleared       int
	RemovedLeases []string
}

type Retention interface {
	PurgeExpiredLocked(now int64) []string
	RetainFloorLocked() int64
}

type History struct {
	mu      sync.RWMutex
	now     int64            // 已接受的最大 now
	maxSeq  int64            // 已分配的最大 seq
	h       int64            // 历史完整下界，seq>=h 的操作必然在
	ops     []*Op            // 下标 seq-1；被清除处置 nil
	latest  map[string]int64 // id -> 当前最新操作 seq（含存活与墓碑）
	alive   map[string]int64 // 存活 id -> 其最新 Index seq
	touched int              // 单次 Merge 触碰的操作记录数（证明用）
}

func NewHistory() *History {
	return &History{
		now:    0,
		h:      1,
		latest: make(map[string]int64),
		alive:  make(map[string]int64),
	}
}

// Lock/Unlock 暴露给 lease 包：租约操作需要与 Merge、Index/Delete 全局串行。
func (h *History) Lock()    { h.mu.Lock() }
func (h *History) Unlock()  { h.mu.Unlock() }
func (h *History) RLock()   { h.mu.RLock() }
func (h *History) RUnlock() { h.mu.RUnlock() }

// CheckNowLocked 只校验时钟不回退，不推进。
func (h *History) CheckNowLocked(now int64) error {
	if now < 0 || now > maxNow || now < h.now {
		return ErrClockBack
	}
	return nil
}

// AcceptNowLocked 校验并推进已接受最大 now（仅成功操作调用）。
func (h *History) AcceptNowLocked(now int64) error {
	if err := h.CheckNowLocked(now); err != nil {
		return err
	}
	h.now = now
	return nil
}

func (h *History) MaxSeqLocked() int64 { return h.maxSeq }
func (h *History) HLocked() int64      { return h.h }

// OpsAfterLocked 返回 seq>c 的全部现存操作（调用方保证 c+1>=H，故连续无洞）。
func (h *History) OpsAfterLocked(c int64) []Op {
	out := make([]Op, 0, h.maxSeq-c)
	for seq := c + 1; seq <= h.maxSeq; seq++ {
		out = append(out, *h.ops[seq-1])
	}
	return out
}

// AliveDocsLocked 返回存活文档，按 id 字节序升序，各带最新 Index seq。
func (h *History) AliveDocsLocked() []LiveDocSnapshot {
	out := make([]LiveDocSnapshot, 0, len(h.alive))
	for id, seq := range h.alive {
		out = append(out, LiveDocSnapshot{ID: []byte(id), Seq: seq})
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].ID, out[j].ID) < 0 })
	return out
}

// LiveDocSnapshot 与 recovery.LiveDoc 结构对齐（恢复包自行转换）。
type LiveDocSnapshot struct {
	ID  []byte
	Seq int64
}

func validID(id []byte) bool {
	return len(id) >= minID && len(id) <= maxID
}

func (h *History) Index(now int64, id []byte) (int64, error) {
	if now < 0 || now > maxNow || !validID(id) {
		return 0, ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.AcceptNowLocked(now); err != nil {
		return 0, err
	}
	h.maxSeq++
	seq := h.maxSeq
	key := string(id)
	op := &Op{Seq: seq, ID: append([]byte(nil), id...), Kind: KindIndex}
	h.ops = append(h.ops, op)
	h.latest[key] = seq
	h.alive[key] = seq
	return seq, nil
}

func (h *History) Delete(now int64, id []byte) (int64, error) {
	if now < 0 || now > maxNow || !validID(id) {
		return 0, ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.CheckNowLocked(now); err != nil {
		return 0, err
	}
	key := string(id)
	if _, ok := h.alive[key]; !ok {
		return 0, ErrDocNotFound
	}
	if err := h.AcceptNowLocked(now); err != nil {
		return 0, err
	}
	h.maxSeq++
	seq := h.maxSeq
	op := &Op{Seq: seq, ID: append([]byte(nil), id...), Kind: KindDelete}
	h.ops = append(h.ops, op)
	h.latest[key] = seq
	delete(h.alive, key)
	return seq, nil
}

func (h *History) Merge(now int64, ret Retention) (MergeResult, error) {
	if now < 0 || now > maxNow {
		return MergeResult{}, ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.AcceptNowLocked(now); err != nil {
		return MergeResult{}, err
	}

	h.touched = 0
	// 1) 先剔除全部过期租约（剔除资格仅在 Merge 开头兑现）。
	removed := ret.PurgeExpiredLocked(now)

	// 2) floor = min(gcp+1, 现存租约最小 r)。
	floor := ret.RetainFloorLocked()

	// 3) 清除 [H, floor) 内的被取代者与 Delete 墓碑；
	//    未被取代的 Index（存活文档）保留。触碰量恰为 floor-H。
	cleared := 0
	for seq := h.h; seq < floor; seq++ {
		op := h.ops[seq-1]
		h.touched++
		if op == nil {
			continue
		}
		superseded := h.latest[string(op.ID)] > op.Seq
		if op.Kind == KindDelete || superseded {
			h.ops[seq-1] = nil
			cleared++
		}
	}

	// 4) H=floor（floor>=H 由 gcp 与租约 r 均不小于 H 保证）。
	h.h = floor
	return MergeResult{Cleared: cleared, RemovedLeases: removed}, nil
}

// TouchedLocked 返回最近一次 Merge 触碰的操作记录数（证明用，单测读取）。
func (h *History) TouchedLocked() int { return h.touched }
