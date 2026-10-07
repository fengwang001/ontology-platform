package incremental

import (
	"errors"
	"fmt"
	"sync"
)

// Options 配置 Exporter 的资源上限。
type Options struct {
	// MaxCycleWrites 是单个周期内允许缓冲的去重后写入数上限，
	// 超过即判定为资源不足（ErrKindResourceExhausted）并中止周期。
	// <= 0 表示不限制。
	MaxCycleWrites int
	// DedupWindow 是跨周期去重“近期窗口”的写入 ID 上限，
	// 用于位点推导回退后的重复检查。<= 0 表示关闭跨周期窗口。
	DedupWindow int
}

// Exporter 在同一本体数据源上编排多条独立导出链路的周期执行。
// 不同链路并发执行互不影响；同一链路内部通过互斥锁串行化，
// 结果等价于该链路单独串行执行。
type Exporter struct {
	source  Source
	cursors CursorStore
	ledger  Ledger
	opts    Options

	mu     sync.Mutex
	chains map[string]*chainState
	// mem 保存本进程最近一次确认或推导出的各链路位点，
	// 用于位点记录介质持续不可读时的进程内恢复。
	mem map[string]Cursor
}

type chainState struct {
	mu     sync.Mutex // 周期级互斥：BeginCycle 获取，Commit/Abort 释放
	output []Write    // 已确认发布的输出序列
}

// NewExporter 创建导出组件。
func NewExporter(source Source, cursors CursorStore, ledger Ledger, opts Options) *Exporter {
	return &Exporter{
		source:  source,
		cursors: cursors,
		ledger:  ledger,
		opts:    opts,
		chains:  make(map[string]*chainState),
		mem:     make(map[string]Cursor),
	}
}

func (e *Exporter) chain(id string) *chainState {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs, ok := e.chains[id]
	if !ok {
		cs = &chainState{}
		e.chains[id] = cs
	}
	return cs
}

// Finding 是周期建立过程中记录的非致命事件（如位点不可读后的安全推导）。
type Finding struct {
	Kind   ErrorKind
	Detail string
}

// Cycle 表示一个已声明起止位点、尚未确认结束位点的导出周期。
type Cycle struct {
	exp      *Exporter
	state    *chainState
	ChainID  string
	Start    Cursor
	End      Cursor
	Findings []Finding

	dedup  *CycleDeduper
	buffer []Write
	done   bool
}

// BeginCycle 为链路开启一个导出周期，声明起始与结束位点。
//
// 判定顺序固定（见 ErrorKind 文档）：
//  1. 位点记录可读时，先校验声明起始位点与已确认结束位点是否一致；
//  2. 位点记录不可读（ErrCursorCorrupt）时，基于历史增量记录推导安全
//     起始位点，记录 ErrKindCursorUnreadable 事件后继续；
//  3. 推导发现历史缺口时返回 ErrKindHistoryGap（错误中携带安全位点）；
//     推导成功后再补做第 1 步的一致性校验，不匹配时仍报告
//     ErrKindStartMismatch（优先级高于已记录的不可读事件）。
//
// 成功时返回的 Cycle 持有链路互斥锁，直到 Commit 或 Abort。
func (e *Exporter) BeginCycle(chainID string, declaredStart, end Cursor) (*Cycle, error) {
	if end < declaredStart {
		return nil, fmt.Errorf("incremental: chain %q: declared end %d before start %d", chainID, end, declaredStart)
	}
	state := e.chain(chainID)
	state.mu.Lock()

	var findings []Finding
	var records []IncrementRecord

	confirmed, err := e.cursors.Load(chainID)
	switch {
	case err == nil:
		// 位点记录可读，直接使用并刷新进程内缓存。
		e.mu.Lock()
		e.mem[chainID] = confirmed
		e.mu.Unlock()
	case errors.Is(err, ErrCursorCorrupt):
		// 位点记录不可读：记录事件；优先使用本进程已确认/推导的
		// 位点，否则基于历史增量记录推导安全位点。
		findings = append(findings, Finding{
			Kind:   ErrKindCursorUnreadable,
			Detail: "cursor record unreadable, deriving safe cursor from increment history",
		})
		e.mu.Lock()
		cached, ok := e.mem[chainID]
		e.mu.Unlock()
		if ok {
			confirmed = cached
			break
		}
		records, err = e.ledger.Records(chainID)
		if err != nil {
			state.mu.Unlock()
			return nil, err
		}
		safe, gerr := DeriveSafeCursor(chainID, records)
		// 推导出的安全位点一定是曾经被确认过的位点（连续已确认
		// 前缀的末端），缓存起来供介质恢复前的后续周期使用。
		e.mu.Lock()
		e.mem[chainID] = safe
		e.mu.Unlock()
		if gerr != nil {
			gerr.Findings = findings
			state.mu.Unlock()
			return nil, gerr
		}
		confirmed = safe
	default:
		state.mu.Unlock()
		return nil, err
	}

	// 起始位点一致性校验：必须等于上一次已确认的结束位点，
	// 不允许凭空选择起始位点。
	if declaredStart != confirmed {
		state.mu.Unlock()
		err := newError(ErrKindStartMismatch, chainID,
			fmt.Sprintf("declared start %d, last confirmed end %d", declaredStart, confirmed))
		err.Findings = findings
		return nil, err
	}

	// 构造去重器：跨周期近期窗口取自已确认历史尾部的写入 ID。
	if records == nil {
		records, err = e.ledger.Records(chainID)
		if err != nil {
			state.mu.Unlock()
			return nil, err
		}
	}
	recent := tailWriteIDs(records, e.opts.DedupWindow)

	return &Cycle{
		exp:      e,
		state:    state,
		ChainID:  chainID,
		Start:    declaredStart,
		End:      end,
		Findings: findings,
		dedup:    NewCycleDeduper(recent, max(e.opts.DedupWindow, 0)),
	}, nil
}

// tailWriteIDs 取已确认记录尾部不超过 cap 个写入 ID（保持原顺序）。
func tailWriteIDs(records []IncrementRecord, cap int) []string {
	if cap <= 0 {
		return nil
	}
	var ids []string
	for i := len(records) - 1; i >= 0 && len(ids) < cap; i-- {
		rec := records[i]
		for j := len(rec.WriteIDs) - 1; j >= 0 && len(ids) < cap; j-- {
			ids = append(ids, rec.WriteIDs[j])
		}
	}
	// 反转为正序。
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}

// Submit 向周期提交一条写入。同一写入因重试被多次提交时只有首次
// 被接受并进入输出缓冲，其位置不随重试改变。写入位点必须落在周期
// 声明的 (Start, End] 区间内。
func (c *Cycle) Submit(w Write) error {
	if c.done {
		return fmt.Errorf("incremental: chain %q: cycle already closed", c.ChainID)
	}
	if w.Cursor <= c.Start || w.Cursor > c.End {
		return fmt.Errorf("incremental: chain %q: write %q cursor %d outside declared range (%d, %d]",
			c.ChainID, w.ID, w.Cursor, c.Start, c.End)
	}
	accepted, _ := c.dedup.Accept(w.ID)
	if !accepted {
		return nil
	}
	if c.exp.opts.MaxCycleWrites > 0 && len(c.buffer) >= c.exp.opts.MaxCycleWrites {
		// 输出过程中资源不足：中止周期，不确认任何位点。
		c.Abort()
		return newError(ErrKindResourceExhausted, c.ChainID,
			fmt.Sprintf("cycle buffer limit %d reached", c.exp.opts.MaxCycleWrites))
	}
	c.buffer = append(c.buffer, w)
	return nil
}

// Commit 确认周期的结束位点：先把增量记录追加到账台（确认的事实
// 来源），再更新位点缓存，最后发布本周期输出。只有在本周期全部应
// 输出的写入都已进入输出缓冲之后才允许调用。
func (c *Cycle) Commit() error {
	if c.done {
		return fmt.Errorf("incremental: chain %q: cycle already closed", c.ChainID)
	}
	defer c.close()
	rec := IncrementRecord{
		Chain:    c.ChainID,
		Start:    c.Start,
		End:      c.End,
		WriteIDs: c.dedup.Order(),
	}
	if _, err := c.exp.ledger.Append(rec); err != nil {
		return err
	}
	if err := c.exp.cursors.Store(c.ChainID, c.End); err != nil {
		return err
	}
	c.exp.mu.Lock()
	c.exp.mem[c.ChainID] = c.End
	c.exp.mu.Unlock()
	c.state.output = append(c.state.output, c.buffer...)
	return nil
}

// Abort 中止周期：丢弃输出缓冲，不确认任何位点。周期内已接受的
// 写入不留下任何位置痕迹。
func (c *Cycle) Abort() {
	if c.done {
		return
	}
	c.close()
}

func (c *Cycle) close() {
	c.done = true
	c.buffer = nil
	c.dedup = nil
	c.state.mu.Unlock()
}

// Buffered 返回本周期当前已缓冲（首次被接受、按接受顺序）的写入。
func (c *Cycle) Buffered() []Write {
	out := make([]Write, len(c.buffer))
	copy(out, c.buffer)
	return out
}

// DedupStats 返回本周期去重器规模快照，用于复核开销有界性。
func (c *Cycle) DedupStats() DedupStats { return c.dedup.Stats() }

// Published 返回链路已确认发布的完整输出序列（拷贝）。
func (e *Exporter) Published(chainID string) []Write {
	state := e.chain(chainID)
	state.mu.Lock()
	defer state.mu.Unlock()
	out := make([]Write, len(state.output))
	copy(out, state.output)
	return out
}

// ConfirmedCursor 返回链路当前已确认的结束位点（直接读位点记录，
// 不做推导；位点不可读时返回错误）。
func (e *Exporter) ConfirmedCursor(chainID string) (Cursor, error) {
	return e.cursors.Load(chainID)
}
