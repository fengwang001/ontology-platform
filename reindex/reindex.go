// Package reindex 实现在线重建索引协调器：回填批、在途双写与切换。
//
// 协调器复用 source 的内部锁作为外层锁（锁序 source → dest），
// 使源写入与其双写构成同一原子步，所有并发调用等价于某个串行顺序。
package reindex

import (
	"errors"

	"ontology/dest"
	"ontology/source"
)

// State 是协调器状态。
type State int

const (
	// Idle 表示未在重建。
	Idle State = iota
	// Running 表示重建进行中（回填与双写并行）。
	Running
	// Switched 表示已切换，源不再接受写入。
	Switched
)

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Running:
		return "Running"
	case Switched:
		return "Switched"
	}
	return "Unknown"
}

var (
	// ErrInvalidParam 表示参数非法（B 或 tol 越界）。
	ErrInvalidParam = errors.New("reindex: invalid parameter")
	// ErrBadState 表示状态不符（含 Start 时 dest 非空）。
	ErrBadState = errors.New("reindex: invalid state")
	// ErrBackfillPending 表示回填尚未完成。
	ErrBackfillPending = errors.New("reindex: backfill not finished")
	// ErrTooManyFailures 表示失败集合大小严格大于容忍度。
	ErrTooManyFailures = errors.New("reindex: failures exceed tolerance")
)

const (
	// MinBatch 是 Step 每批回填的最小条数。
	MinBatch = 1
	// MaxBatch 是 Step 每批回填的最大条数。
	MaxBatch = 1000
	// MaxTol 是 Cutover 容忍度的最大值。
	MaxTol = 1000000
)

// Coordinator 协调一次在线重建。
type Coordinator struct {
	src *source.Source
	dst *dest.Dest

	state    State
	batch    int
	snapshot []source.Doc
	scanned  uint64 // 非导出计数器：Step 实际读取的快照条目数
}

// New 构造协调器。
func New(src *source.Source, dst *dest.Dest) *Coordinator {
	return &Coordinator{src: src, dst: dst}
}

// State 返回当前状态。
func (c *Coordinator) State() State {
	c.src.Lock()
	defer c.src.Unlock()
	return c.state
}

// Start 开始重建：要求 Idle 且 dest 无任何记录。
// 冻结此刻源存活文档（按 id 字节序）为快照，并开启在途双写。
// 拒绝次序：参数非法 > 状态不符。
func (c *Coordinator) Start(B int) error {
	if B < MinBatch || B > MaxBatch {
		return ErrInvalidParam
	}
	c.src.Lock()
	defer c.src.Unlock()
	if c.state != Idle || !c.dst.Empty() {
		return ErrBadState
	}
	c.snapshot = c.src.BeginForward(func(del bool, id string, body []byte, seq uint64) {
		if del {
			c.dst.Delete(id, seq)
		} else {
			c.dst.Index(id, body, seq)
		}
	})
	c.batch = B
	c.scanned = 0
	c.state = Running
	return nil
}

// Step 回填快照中接下来的至多 B 条，以快照里的 seq 为 ver 逐条 Index
// 到 dest，返回（应用数, 冲突数, 不兼容数, 是否已回填完）。
// 回填完之后再调用返回全 0 与真，不改状态。
func (c *Coordinator) Step() (applied, conflicts, incompatible int, done bool, err error) {
	c.src.Lock()
	defer c.src.Unlock()
	if c.state != Running {
		return 0, 0, 0, false, ErrBadState
	}
	rest := len(c.snapshot) - int(c.scanned)
	n := c.batch
	if rest < n {
		n = rest
	}
	base := int(c.scanned)
	for i := 0; i < n; i++ {
		doc := c.snapshot[base+i]
		a, inc := c.dst.Index(doc.ID, doc.Body, doc.Seq)
		switch {
		case inc:
			incompatible++
		case a:
			applied++
		default:
			conflicts++
		}
	}
	c.scanned += uint64(n)
	return applied, conflicts, incompatible, int(c.scanned) == len(c.snapshot), nil
}

// Cutover 尝试切换。拒绝次序：
// 参数非法 > 状态不是 Running > 回填未完成 > 失败集合大小严格大于 tol。
// 成功则清除 dest 全部墓碑、转 Switched，此后源的 Put/Delete 报已切换。
func (c *Coordinator) Cutover(tol int) error {
	if tol < 0 || tol > MaxTol {
		return ErrInvalidParam
	}
	c.src.Lock()
	defer c.src.Unlock()
	if c.state != Running {
		return ErrBadState
	}
	if int(c.scanned) < len(c.snapshot) {
		return ErrBackfillPending
	}
	if len(c.dst.Failures()) > tol {
		return ErrTooManyFailures
	}
	c.dst.ClearTombstones()
	c.src.EndForward(true)
	c.snapshot = nil
	c.state = Switched
	return nil
}

// Abort 中止重建：清空 dest 回到 Idle。
func (c *Coordinator) Abort() error {
	c.src.Lock()
	defer c.src.Unlock()
	if c.state != Running {
		return ErrBadState
	}
	c.src.EndForward(false)
	c.dst.Reset()
	c.snapshot = nil
	c.state = Idle
	return nil
}
