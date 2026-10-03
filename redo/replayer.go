// Package redo 实现带小事务（MTR）原子性与文件操作屏障的重做日志并行回放器。
package redo

import (
	"fmt"
	"sync"
)

// 合法取值范围。
const (
	MaxSpacePage = 1_000_000             // space 与 page 的上界（含）
	MaxLSN       = 1_000_000_000_000_000 // lsn 的上界（含）
	MaxWorkers   = 64                    // 回放线程数 W 的上界
	MaxBatch     = 1_000_000             // 批次记录上限 M 的上界
)

// Kind 为文件操作类型。
type Kind int

const (
	Create Kind = iota // 创建表空间
	Delete             // 删除表空间
)

// RecType 为日志记录类型。
type RecType int

const (
	PageRec RecType = iota // 页修改记录
	FileRec                // 文件操作记录（自成单记录小事务）
	EndRec                 // 小事务结束记录
)

// Record 为一条重做日志记录。
type Record struct {
	Type  RecType
	LSN   int64
	MTR   int
	Space int
	Page  int
	Delta int64
	Kind  Kind
}

// PageRecord 构造一条页修改记录。
func PageRecord(lsn int64, mtr, space, page int, delta int64) Record {
	return Record{Type: PageRec, LSN: lsn, MTR: mtr, Space: space, Page: page, Delta: delta}
}

// FileRecord 构造一条文件操作记录。
func FileRecord(lsn int64, mtr int, kind Kind, space int) Record {
	return Record{Type: FileRec, LSN: lsn, MTR: mtr, Kind: kind, Space: space}
}

// EndRecord 构造一条小事务结束记录。
func EndRecord(lsn int64, mtr int) Record {
	return Record{Type: EndRec, LSN: lsn, MTR: mtr}
}

// ErrCode 为拒绝原因码，按声明顺序检查，只报第一个。
type ErrCode int

const (
	ErrInvalidArgs ErrCode = iota // 参数非法
	ErrFinished                   // 回放已结束
	ErrStarted                    // 回放已开始
	ErrLSN                        // LSN 不够大
	ErrMTR                        // 小事务错误
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidArgs:
		return "参数非法"
	case ErrFinished:
		return "回放已结束"
	case ErrStarted:
		return "回放已开始"
	case ErrLSN:
		return "LSN 不够大"
	case ErrMTR:
		return "小事务错误"
	}
	return "未知错误"
}

// Error 为被拒绝操作的错误，Code 可区分拒绝原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// Result 为一条页记录的处理结果。
type Result int

const (
	Applied      Result = iota // 应用
	Skipped                    // 已应用（lsn 不大于 pageLSN，跳过）
	SpaceMissing               // 空间缺失（丢弃）
)

func (r Result) String() string {
	switch r {
	case Applied:
		return "应用"
	case Skipped:
		return "已应用"
	case SpaceMissing:
		return "空间缺失"
	}
	return "未知结果"
}

// ApplyEntry 为 ApplyLog 中的一项，按实际处理次序排列。
type ApplyEntry struct {
	LSN    int64
	Result Result
}

// Stats 为回放统计计数。
type Stats struct {
	Batches   int // 已执行的批次数
	Accepted  int // 已接受的 Page 记录数
	Applied   int // 应用数
	Skipped   int // 已应用（跳过）数
	Missing   int // 空间缺失数
	Discarded int // 被丢弃数（Finish 时未 End 的暂存记录）
	Pending   int // 各分发队列中的记录总数
	Staged    int // 未 End 的小事务暂存记录数
}

type pageState struct {
	value int64
	lsn   int64
}

// Replayer 为重做日志并行回放器。所有方法可并发调用，效果等价于某个串行顺序。
type Replayer struct {
	mu sync.Mutex

	w int
	m int

	spaces map[int]map[int]*pageState

	queues   [][]Record // 每个回放线程一个队列，队列内 LSN 升序
	pending  int
	staged   []Record // 当前未 End 小事务的暂存记录
	openMTR  int
	hasOpen  bool
	maxMTR   int
	maxLSN   int64
	started  bool
	finished bool

	applyLog  []ApplyEntry
	batches   int
	accepted  int
	applied   int
	skipped   int
	missing   int
	discarded int
}

// NewReplayer 构造回放器，w 为回放线程数（1..64），m 为批次记录上限（1..1e6）。
func NewReplayer(w, m int) (*Replayer, error) {
	if w < 1 || w > MaxWorkers {
		return nil, &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("回放线程数 %d 不在 [1,%d]", w, MaxWorkers)}
	}
	if m < 1 || m > MaxBatch {
		return nil, &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("批次记录上限 %d 不在 [1,%d]", m, MaxBatch)}
	}
	return &Replayer{
		w:      w,
		m:      m,
		spaces: make(map[int]map[int]*pageState),
		queues: make([][]Record, w),
	}, nil
}

// LoadPage 在回放开始前加载磁盘页映像，并使该表空间存在。
func (r *Replayer) LoadPage(space, page int, value, lsn int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if space < 0 || space > MaxSpacePage || page < 0 || page > MaxSpacePage {
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("space/page 越界: (%d,%d)", space, page)}
	}
	if lsn < 0 || lsn > MaxLSN {
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("load lsn %d 不在 [0,%d]", lsn, MaxLSN)}
	}
	if r.finished {
		return &Error{Code: ErrFinished, Msg: "回放已结束，拒绝 LoadPage"}
	}
	if r.started {
		return &Error{Code: ErrStarted, Msg: "回放已开始，拒绝 LoadPage"}
	}
	pages := r.spaces[space]
	if pages == nil {
		pages = make(map[int]*pageState)
		r.spaces[space] = pages
	}
	pages[page] = &pageState{value: value, lsn: lsn}
	return nil
}

// Feed 接受一条日志记录；被拒绝时不改变任何状态。
func (r *Replayer) Feed(rec Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := rec.validate(); err != nil {
		return err
	}
	if r.finished {
		return &Error{Code: ErrFinished, Msg: "回放已结束，拒绝 Feed"}
	}
	if rec.LSN <= r.maxLSN {
		return &Error{Code: ErrLSN, Msg: fmt.Sprintf("lsn %d 不大于已接受的最大 lsn %d", rec.LSN, r.maxLSN)}
	}
	var err error
	switch rec.Type {
	case PageRec:
		err = r.feedPageLocked(rec)
	case FileRec:
		err = r.feedFileLocked(rec)
	case EndRec:
		err = r.feedEndLocked(rec)
	}
	if err != nil {
		return err
	}
	r.maxLSN = rec.LSN
	r.started = true
	return nil
}

func (rec Record) validate() error {
	switch rec.Type {
	case PageRec, FileRec, EndRec:
	default:
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("记录类型非法: %d", rec.Type)}
	}
	if rec.LSN < 1 || rec.LSN > MaxLSN {
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("lsn %d 不在 [1,%d]", rec.LSN, MaxLSN)}
	}
	if rec.MTR < 1 {
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("mtr 号 %d 小于 1", rec.MTR)}
	}
	if rec.Type == PageRec || rec.Type == FileRec {
		if rec.Space < 0 || rec.Space > MaxSpacePage {
			return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("space %d 越界", rec.Space)}
		}
	}
	if rec.Type == PageRec {
		if rec.Page < 0 || rec.Page > MaxSpacePage {
			return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("page %d 越界", rec.Page)}
		}
	}
	if rec.Type == FileRec && rec.Kind != Create && rec.Kind != Delete {
		return &Error{Code: ErrInvalidArgs, Msg: fmt.Sprintf("kind 非法: %d", rec.Kind)}
	}
	return nil
}

// feedPageLocked 处理页记录：先暂存，End 到达时才入队。
func (r *Replayer) feedPageLocked(rec Record) error {
	if r.hasOpen {
		if rec.MTR != r.openMTR {
			return &Error{Code: ErrMTR, Msg: fmt.Sprintf("Page 的 mtr %d 与未结束的 mtr %d 不同", rec.MTR, r.openMTR)}
		}
	} else {
		if rec.MTR <= r.maxMTR {
			return &Error{Code: ErrMTR, Msg: fmt.Sprintf("新开 mtr 号 %d 不大于此前最大号 %d", rec.MTR, r.maxMTR)}
		}
		r.openMTR = rec.MTR
		r.hasOpen = true
		r.maxMTR = rec.MTR
	}
	r.staged = append(r.staged, rec)
	r.accepted++
	return nil
}

// feedEndLocked 处理 End：暂存记录进入分发队列，pending 达到 M 立即做一个批次。
func (r *Replayer) feedEndLocked(rec Record) error {
	if !r.hasOpen {
		return &Error{Code: ErrMTR, Msg: fmt.Sprintf("End 的 mtr %d 没有对应的未结束小事务", rec.MTR)}
	}
	if rec.MTR != r.openMTR {
		return &Error{Code: ErrMTR, Msg: fmt.Sprintf("End 的 mtr %d 与未结束的 mtr %d 不一致", rec.MTR, r.openMTR)}
	}
	for _, s := range r.staged {
		w := workerOf(s.Space, s.Page, r.w)
		r.queues[w] = append(r.queues[w], s)
	}
	r.pending += len(r.staged)
	r.staged = r.staged[:0]
	r.hasOpen = false
	if r.pending >= r.m {
		r.batchLocked()
	}
	return nil
}

// feedFileLocked 处理文件操作：先做批次屏障，再立即执行文件操作。
func (r *Replayer) feedFileLocked(rec Record) error {
	if r.hasOpen {
		return &Error{Code: ErrMTR, Msg: fmt.Sprintf("File 出现在未结束的 mtr %d 之中", r.openMTR)}
	}
	if rec.MTR <= r.maxMTR {
		return &Error{Code: ErrMTR, Msg: fmt.Sprintf("File 的 mtr 号 %d 不大于此前最大号 %d", rec.MTR, r.maxMTR)}
	}
	r.maxMTR = rec.MTR
	if r.pending > 0 {
		r.batchLocked()
	}
	switch rec.Kind {
	case Create:
		if _, ok := r.spaces[rec.Space]; !ok {
			r.spaces[rec.Space] = make(map[int]*pageState)
		}
	case Delete:
		delete(r.spaces, rec.Space)
	}
	return nil
}

// batchLocked 按线程号 0..W-1 依次处理各队列，队列内按 LSN 升序逐条应用。
func (r *Replayer) batchLocked() {
	for w := 0; w < r.w; w++ {
		for _, rec := range r.queues[w] {
			res := r.applyOneLocked(rec)
			r.applyLog = append(r.applyLog, ApplyEntry{LSN: rec.LSN, Result: res})
		}
		r.queues[w] = r.queues[w][:0]
	}
	r.pending = 0
	r.batches++
}

func (r *Replayer) applyOneLocked(rec Record) Result {
	pages, ok := r.spaces[rec.Space]
	if !ok {
		r.missing++
		return SpaceMissing
	}
	p := pages[rec.Page]
	if p == nil {
		p = &pageState{}
		pages[rec.Page] = p
	}
	if rec.LSN <= p.lsn {
		r.skipped++
		return Skipped
	}
	p.value += rec.Delta
	p.lsn = rec.LSN
	r.applied++
	return Applied
}

func workerOf(space, page, w int) int {
	return (space*7 + page) % w
}

// Finish 结束回放：先做一个批次（pending 大于 0 时），再丢弃未 End 的暂存记录。
func (r *Replayer) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return &Error{Code: ErrFinished, Msg: "回放已结束，拒绝再次 Finish"}
	}
	if r.pending > 0 {
		r.batchLocked()
	}
	r.discarded += len(r.staged)
	r.staged = nil
	r.hasOpen = false
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

// PageState 查询页当前的 value 与 pageLSN；未加载过的页为 0 与 0。
func (r *Replayer) PageState(space, page int) (value, pageLSN int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pages, ok := r.spaces[space]; ok {
		if p, ok := pages[page]; ok {
			return p.value, p.lsn
		}
	}
	return 0, 0
}

// Stats 返回当前统计计数。
func (r *Replayer) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		Batches:   r.batches,
		Accepted:  r.accepted,
		Applied:   r.applied,
		Skipped:   r.skipped,
		Missing:   r.missing,
		Discarded: r.discarded,
		Pending:   r.pending,
		Staged:    len(r.staged),
	}
}
