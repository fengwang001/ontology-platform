// Package redolog 实现带小事务（MTR）原子性与文件操作屏障的重做日志并行回放器。
//
// 已结束的小事务的页记录按页哈希分发到 W 个回放线程队列，pending 达到 M 时
// 触发批次；批次内按线程号 0..W-1 依次、队列内按 LSN 升序应用记录。File 记录
// 充当屏障：先冲刷当前批次，再立即执行文件操作。最终页状态与严格按 LSN 升序
// 逐条处理全部已 End 的小事务记录的顺序回放一致，与 W、M 取值无关。
package redolog

import (
	"fmt"
	"sync"
)

const (
	maxSpacePage = 1_000_000
	maxLSN       = 1_000_000_000_000_000
	maxWorkers   = 64
	maxBatch     = 1_000_000
)

// RecordType 标识日志记录类别。
type RecordType int

const (
	RecPage RecordType = iota // 页记录，属于某个小事务
	RecFile                   // 文件操作记录，自成单记录小事务
	RecEnd                    // 小事务结束记录
)

// FileKind 标识文件操作种类。
type FileKind int

const (
	FileCreate FileKind = iota // 创建表空间
	FileDelete                 // 删除表空间
)

// Record 是一条待回放的日志记录。按 Type 使用相应字段：
//   - RecPage: LSN、Mtr、Space、Page、Delta
//   - RecFile: LSN、Mtr、Kind、Space
//   - RecEnd:  LSN、Mtr
type Record struct {
	Type  RecordType
	LSN   int64
	Mtr   int64
	Space int64
	Page  int64
	Delta int64
	Kind  FileKind
}

// ApplyResult 是一条页记录在批次中的处理结果。
type ApplyResult int

const (
	Applied        ApplyResult = iota // 应用：lsn 大于 pageLSN，已累加并推进 pageLSN
	AlreadyApplied                    // 已应用：lsn 不大于 pageLSN（恰等也算），跳过
	SpaceMissing                      // 空间缺失：表空间不存在，丢弃
)

func (r ApplyResult) String() string {
	switch r {
	case Applied:
		return "应用"
	case AlreadyApplied:
		return "已应用"
	case SpaceMissing:
		return "空间缺失"
	}
	return "未知"
}

// ApplyEntry 是 ApplyLog 中的一项，按实际处理次序记录。
type ApplyEntry struct {
	LSN    int64
	Result ApplyResult
}

// ErrReason 是可区分的拒绝原因。
type ErrReason int

const (
	ErrInvalidParam ErrReason = iota // 参数非法
	ErrFinished                      // 回放已结束
	ErrStarted                       // 回放已开始（Load 在 Feed 之后）
	ErrLSNTooSmall                   // LSN 不够大
	ErrMtr                           // 小事务错误
)

func (r ErrReason) String() string {
	switch r {
	case ErrInvalidParam:
		return "参数非法"
	case ErrFinished:
		return "回放已结束"
	case ErrStarted:
		return "回放已开始"
	case ErrLSNTooSmall:
		return "LSN 不够大"
	case ErrMtr:
		return "小事务错误"
	}
	return "未知"
}

// Error 是回放器返回的错误，Reason 区分拒绝原因。
type Error struct {
	Reason  ErrReason
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(reason ErrReason, format string, args ...any) *Error {
	return &Error{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// Stats 是回放器的计数快照。不变式：
// AcceptedPages == Applied + AlreadyApplied + SpaceMissing + Discarded + Buffered。
type Stats struct {
	AcceptedPages  int64 // 已接受的 Page 记录总数
	Applied        int64 // 应用数
	AlreadyApplied int64 // 已应用（跳过）数
	SpaceMissing   int64 // 空间缺失数
	Discarded      int64 // Finish 时被丢弃的未 End 暂存记录数
	Buffered       int64 // 暂存中或已入队等待批次的记录数
}

type pageState struct {
	value int64
	lsn   int64
}

type pageRec struct {
	lsn   int64
	space int64
	page  int64
	delta int64
}

// Replayer 是重做日志并行回放器。所有方法均可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Replayer struct {
	mu         sync.Mutex
	workers    int
	batchLimit int

	spaces map[int64]map[int64]pageState

	queues  [][]pageRec
	pending int
	batches int

	applyLog []ApplyEntry

	started  bool
	finished bool
	maxLSN   int64
	maxMtr   int64
	hasOpen  bool
	openMtr  int64
	staged   []pageRec

	acceptedPages  int64
	applied        int64
	alreadyApplied int64
	spaceMissing   int64
	discarded      int64
}

// NewReplayer 构造回放器。workers 为回放线程数（1..64），
// batchLimit 为批次记录上限 M（1..10^6）。
func NewReplayer(workers, batchLimit int) (*Replayer, error) {
	if workers < 1 || workers > maxWorkers {
		return nil, newError(ErrInvalidParam, "回放线程数 %d 越界（须为 1..%d）", workers, maxWorkers)
	}
	if batchLimit < 1 || batchLimit > maxBatch {
		return nil, newError(ErrInvalidParam, "批次记录上限 %d 越界（须为 1..%d）", batchLimit, maxBatch)
	}
	return &Replayer{
		workers:    workers,
		batchLimit: batchLimit,
		spaces:     make(map[int64]map[int64]pageState),
		queues:     make([][]pageRec, workers),
	}, nil
}

func validSpacePage(v int64) bool { return v >= 0 && v <= maxSpacePage }

// LoadPage 在回放开始前加载磁盘页映像，并使该表空间存在。
func (r *Replayer) LoadPage(space, page, value, lsn int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !validSpacePage(space) || !validSpacePage(page) {
		return newError(ErrInvalidParam, "space/page 越界（须为 0..%d）", maxSpacePage)
	}
	if lsn < 0 || lsn > maxLSN {
		return newError(ErrInvalidParam, "Load 的 lsn %d 越界（须为 0..%d）", lsn, maxLSN)
	}
	if r.finished {
		return newError(ErrFinished, "回放已结束，拒绝 LoadPage")
	}
	if r.started {
		return newError(ErrStarted, "回放已开始，拒绝 LoadPage")
	}
	sp, ok := r.spaces[space]
	if !ok {
		sp = make(map[int64]pageState)
		r.spaces[space] = sp
	}
	sp[page] = pageState{value: value, lsn: lsn}
	return nil
}

func validateRecord(rec Record) error {
	switch rec.Type {
	case RecPage, RecFile, RecEnd:
	default:
		return newError(ErrInvalidParam, "未知记录类型 %d", rec.Type)
	}
	if rec.LSN < 1 || rec.LSN > maxLSN {
		return newError(ErrInvalidParam, "lsn %d 越界（须为 1..%d）", rec.LSN, maxLSN)
	}
	if rec.Mtr < 1 {
		return newError(ErrInvalidParam, "mtr %d 小于 1", rec.Mtr)
	}
	switch rec.Type {
	case RecPage:
		if !validSpacePage(rec.Space) || !validSpacePage(rec.Page) {
			return newError(ErrInvalidParam, "space/page 越界（须为 0..%d）", maxSpacePage)
		}
	case RecFile:
		if rec.Kind != FileCreate && rec.Kind != FileDelete {
			return newError(ErrInvalidParam, "kind %d 非法", rec.Kind)
		}
		if !validSpacePage(rec.Space) {
			return newError(ErrInvalidParam, "space 越界（须为 0..%d）", maxSpacePage)
		}
	}
	return nil
}

// Feed 接受一条日志记录。被拒绝的记录不改变任何状态。
func (r *Replayer) Feed(rec Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateRecord(rec); err != nil {
		return err
	}
	if r.finished {
		return newError(ErrFinished, "回放已结束，拒绝 Feed")
	}
	if rec.LSN <= r.maxLSN {
		return newError(ErrLSNTooSmall, "lsn %d 不大于已接受的最大 lsn %d", rec.LSN, r.maxLSN)
	}
	switch rec.Type {
	case RecPage:
		if r.hasOpen {
			if rec.Mtr != r.openMtr {
				return newError(ErrMtr, "Page 的 mtr %d 与未结束的 mtr %d 不同", rec.Mtr, r.openMtr)
			}
		} else {
			if rec.Mtr <= r.maxMtr {
				return newError(ErrMtr, "新开 mtr 号 %d 不大于此前最大号 %d", rec.Mtr, r.maxMtr)
			}
			r.hasOpen = true
			r.openMtr = rec.Mtr
		}
		r.staged = append(r.staged, pageRec{lsn: rec.LSN, space: rec.Space, page: rec.Page, delta: rec.Delta})
		r.acceptedPages++
	case RecEnd:
		if !r.hasOpen || rec.Mtr != r.openMtr {
			return newError(ErrMtr, "End 的 mtr %d 与未结束的小事务不一致", rec.Mtr)
		}
		for _, pr := range r.staged {
			w := int((pr.space*7 + pr.page) % int64(r.workers))
			r.queues[w] = append(r.queues[w], pr)
		}
		r.pending += len(r.staged)
		r.staged = r.staged[:0]
		r.hasOpen = false
		if r.pending >= r.batchLimit {
			r.doBatchLocked()
		}
	case RecFile:
		if r.hasOpen {
			return newError(ErrMtr, "File 出现在未结束的 mtr %d 之中", r.openMtr)
		}
		if rec.Mtr <= r.maxMtr {
			return newError(ErrMtr, "File 的 mtr 号 %d 不大于此前最大号 %d", rec.Mtr, r.maxMtr)
		}
		if r.pending > 0 {
			r.doBatchLocked()
		}
		r.execFileLocked(rec.Kind, rec.Space)
	}
	r.maxLSN = rec.LSN
	if rec.Mtr > r.maxMtr {
		r.maxMtr = rec.Mtr
	}
	r.started = true
	return nil
}

func (r *Replayer) execFileLocked(kind FileKind, space int64) {
	switch kind {
	case FileCreate:
		if _, ok := r.spaces[space]; !ok {
			r.spaces[space] = make(map[int64]pageState)
		}
	case FileDelete:
		delete(r.spaces, space)
	}
}

// doBatchLocked 处理一个批次：按线程号 0..W-1 依次、队列内按 LSN 升序逐条应用。
// 调用前必须持有锁，且 pending 大于 0。
func (r *Replayer) doBatchLocked() {
	for w := 0; w < r.workers; w++ {
		for _, rec := range r.queues[w] {
			res := r.applyOneLocked(rec)
			r.applyLog = append(r.applyLog, ApplyEntry{LSN: rec.lsn, Result: res})
		}
		r.queues[w] = r.queues[w][:0]
	}
	r.pending = 0
	r.batches++
}

func (r *Replayer) applyOneLocked(rec pageRec) ApplyResult {
	sp, ok := r.spaces[rec.space]
	if !ok {
		r.spaceMissing++
		return SpaceMissing
	}
	p := sp[rec.page]
	if rec.lsn <= p.lsn {
		r.alreadyApplied++
		return AlreadyApplied
	}
	p.value += rec.delta
	p.lsn = rec.lsn
	sp[rec.page] = p
	r.applied++
	return Applied
}

// Finish 结束回放：pending 大于 0 时先做一个批次，
// 再丢弃尚未 End 的小事务的全部暂存记录。
func (r *Replayer) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return newError(ErrFinished, "回放已结束，拒绝重复 Finish")
	}
	if r.pending > 0 {
		r.doBatchLocked()
	}
	if r.hasOpen {
		r.discarded += int64(len(r.staged))
		r.staged = nil
		r.hasOpen = false
	}
	r.finished = true
	return nil
}

// ApplyLog 返回按实际处理次序的（lsn、结果）列表。
func (r *Replayer) ApplyLog() []ApplyEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ApplyEntry, len(r.applyLog))
	copy(out, r.applyLog)
	return out
}

// Pending 返回当前各分发队列中的记录总数。
func (r *Replayer) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

// Batches 返回已执行的批次数。
func (r *Replayer) Batches() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.batches
}

// Stats 返回计数快照。
func (r *Replayer) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		AcceptedPages:  r.acceptedPages,
		Applied:        r.applied,
		AlreadyApplied: r.alreadyApplied,
		SpaceMissing:   r.spaceMissing,
		Discarded:      r.discarded,
		Buffered:       int64(len(r.staged)) + int64(r.pending),
	}
}

// PageState 查询页状态。spaceExists 为 false 表示表空间不存在；
// 存在但未加载/未写过的页返回 value=0、pageLSN=0。
func (r *Replayer) PageState(space, page int64) (value, pageLSN int64, spaceExists bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sp, ok := r.spaces[space]
	if !ok {
		return 0, 0, false
	}
	p := sp[page]
	return p.value, p.lsn, true
}
