package gate

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"sort"
	"sync"
)

// 可区分的操作拒绝原因。
var (
	ErrPaused       = errors.New("gate: channel paused")
	ErrInvalidBatch = errors.New("gate: invalid batch")
	ErrSeqExists    = errors.New("gate: sequence already exists")
	ErrSeqNotFound  = errors.New("gate: sequence not found")
	ErrNotQuarantin = errors.New("gate: batch is not in quarantine")
	ErrAlreadyRel   = errors.New("gate: batch already released")
	ErrAlreadyDisc  = errors.New("gate: batch already discarded")
)

// Config 描述质量门禁的规则参数。
type Config struct {
	MinRows     int64    // 规则一：行数下限（低于该值阻断）
	MaxNullRate Rational // 规则二：空值率上限（严格大于阻断）
	BaselineK   int      // 基线批次数量上限 K（取序号最大的至多 K 个）
	WarnLoPct   int64    // 规则三：行数低于基线中位数 lo% 告警（百分比）
	WarnHiPct   int64    // 规则三：行数高于基线中位数 hi% 告警（百分比）
	MaxBlocksM  int      // 连续阻断 M 次后通道暂停
}

// Rational 表示有理数 Num/Den，用于空值率上限，避免浮点误差。
type Rational struct {
	Num int64
	Den int64
}

// RuleID 标识规则编号。
type RuleID int

const (
	RuleMinRows  RuleID = 1 // 规则一：行数低于下限（阻断）
	RuleNullRate RuleID = 2 // 规则二：空值率超限（阻断）
	RuleBaseline RuleID = 3 // 规则三：行数偏离基线（告警）
)

// BatchState 表示一个序号当前所处的状态。
type BatchState int

const (
	StateUnknown          BatchState = iota // 从未见过该序号
	StateReleased                           // 已放行（含告警放行）
	StateQuarantined                        // 在隔离区
	StateManuallyReleased                   // 人工放行
	StateDiscarded                          // 已丢弃
)

// Decision 是一次提交或重投的裁决结果。
type Decision struct {
	Seq        int64
	Rows       int64
	NullRows   int64
	Allowed    bool     // 是否放行
	Warning    bool     // 是否带告警放行
	Violations []RuleID // 未满足的规则，按规则次序列出
	State      BatchState
}

// Snapshot 是门禁整体状态的查询结果。
type Snapshot struct {
	Paused             bool
	ConsecutiveBlocked int
	KnownSeqs          map[int64]BatchState
}

type batch struct {
	seq      int64
	rows     int64
	nullRows int64
	state    BatchState
}

// Gate 是并发安全的数据批次质量门禁。
// 单一互斥锁保护全部状态，保证所有操作等价于某个串行顺序。
type Gate struct {
	mu sync.Mutex

	cfg    Config
	known  map[int64]*batch // 所有出现过的序号
	passed []int64          // 已放行序号（含告警放行、人工放行），保持升序

	paused bool
	blocks int // 连续阻断数（按裁决顺序，含重投裁决）

	logger *log.Logger
}

// New 创建门禁。非法配置返回错误。
func New(cfg Config) (*Gate, error) {
	if cfg.MinRows < 0 {
		return nil, fmt.Errorf("gate: MinRows must be >= 0")
	}
	if cfg.MaxNullRate.Num < 0 || cfg.MaxNullRate.Den <= 0 {
		return nil, fmt.Errorf("gate: MaxNullRate must be non-negative with positive denominator")
	}
	if cfg.BaselineK <= 0 {
		return nil, fmt.Errorf("gate: BaselineK must be > 0")
	}
	if cfg.WarnLoPct < 0 || cfg.WarnHiPct < 0 {
		return nil, fmt.Errorf("gate: WarnLoPct/WarnHiPct must be >= 0")
	}
	return &Gate{
		cfg:    cfg,
		known:  make(map[int64]*batch),
		passed: nil,
		logger: log.New(os.Stderr, "[gate] ", log.LstdFlags|log.Lmicroseconds),
	}, nil
}

// SetLogger 替换日志输出（传 nil 关闭日志）。
func (g *Gate) SetLogger(w io.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w == nil {
		g.logger = log.New(io.Discard, "", 0)
	} else {
		g.logger = log.New(w, "[gate] ", log.LstdFlags|log.Lmicroseconds)
	}
}

func validBatch(seq, rows, nullRows int64) bool {
	return seq > 0 && rows >= 0 && nullRows >= 0 && nullRows <= rows
}

func (g *Gate) logf(format string, args ...any) {
	g.logger.Printf(format, args...)
}

// Submit 提交一个批次并裁决。
// 校验次序：通道暂停 -> 批次非法 -> 序号已存在。
func (g *Gate) Submit(seq, rows, nullRows int64) (Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.paused {
		g.logf("SUBMIT reject seq=%d rows=%d null=%d reason=channel_paused", seq, rows, nullRows)
		return Decision{}, ErrPaused
	}
	if !validBatch(seq, rows, nullRows) {
		g.logf("SUBMIT reject seq=%d rows=%d null=%d reason=invalid_batch", seq, rows, nullRows)
		return Decision{}, ErrInvalidBatch
	}
	if _, ok := g.known[seq]; ok {
		g.logf("SUBMIT reject seq=%d rows=%d null=%d reason=seq_exists", seq, rows, nullRows)
		return Decision{}, ErrSeqExists
	}

	b := &batch{seq: seq, rows: rows, nullRows: nullRows}
	g.known[seq] = b
	d := g.adjudicate(b)
	g.commitDecision(b, d, "SUBMIT")
	return d, nil
}

// Resubmit 重投隔离区批次（同序号、新数据，按该序号位置重新裁决基线）。
// 校验次序：通道暂停 -> 批次非法 -> 序号不存在/不在隔离区（原因可区分）。
func (g *Gate) Resubmit(seq, rows, nullRows int64) (Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.paused {
		g.logf("RESUBMIT reject seq=%d rows=%d null=%d reason=channel_paused", seq, rows, nullRows)
		return Decision{}, ErrPaused
	}
	if !validBatch(seq, rows, nullRows) {
		g.logf("RESUBMIT reject seq=%d rows=%d null=%d reason=invalid_batch", seq, rows, nullRows)
		return Decision{}, ErrInvalidBatch
	}
	if err := g.checkQuarantined(seq, "RESUBMIT"); err != nil {
		return Decision{}, err
	}

	b := g.known[seq]
	b.rows = rows
	b.nullRows = nullRows
	d := g.adjudicate(b)
	g.commitDecision(b, d, "RESUBMIT")
	return d, nil
}

// ManuallyRelease 将隔离区批次人工放行并进入基线。
// 不受暂停影响；不计入连续阻断数，也不清零。
func (g *Gate) ManuallyRelease(seq int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.checkQuarantined(seq, "MANUAL_RELEASE"); err != nil {
		return err
	}
	b := g.known[seq]
	b.state = StateManuallyReleased
	g.insertPassed(seq)
	g.logf("MANUAL_RELEASE ok seq=%d rows=%d baseline_size=%d blocks=%d(unchanged)",
		seq, b.rows, len(g.passed), g.blocks)
	return nil
}

// Discard 丢弃隔离区批次，此后该序号拒绝一切后续操作。
// 不受暂停影响；不计入连续阻断数，也不清零。
func (g *Gate) Discard(seq int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.checkQuarantined(seq, "DISCARD"); err != nil {
		return err
	}
	b := g.known[seq]
	b.state = StateDiscarded
	g.logf("DISCARD ok seq=%d rows=%d blocks=%d(unchanged)", seq, b.rows, g.blocks)
	return nil
}

// Resume 恢复暂停的通道并清零连续阻断数。
func (g *Gate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()

	wasPaused := g.paused
	g.paused = false
	g.blocks = 0
	g.logf("RESUME ok was_paused=%v blocks_reset=0", wasPaused)
}

// Query 查询单个序号的状态；从未出现返回 ok=false。
func (g *Gate) Query(seq int64) (BatchState, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok := g.known[seq]
	if !ok {
		return StateUnknown, false
	}
	return b.state, true
}

// Snapshot 返回整体状态快照（副本）。
func (g *Gate) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()

	seqs := make(map[int64]BatchState, len(g.known))
	for s, b := range g.known {
		seqs[s] = b.state
	}
	return Snapshot{
		Paused:             g.paused,
		ConsecutiveBlocked: g.blocks,
		KnownSeqs:          seqs,
	}
}

// checkQuarantined 按 不存在 -> 已放行 -> 已丢弃 -> 不在隔离区 的次序给出可区分原因。
func (g *Gate) checkQuarantined(seq int64, op string) error {
	b, ok := g.known[seq]
	if !ok {
		g.logf("%s reject seq=%d reason=not_found", op, seq)
		return ErrSeqNotFound
	}
	switch b.state {
	case StateReleased, StateManuallyReleased:
		g.logf("%s reject seq=%d reason=already_released", op, seq)
		return ErrAlreadyRel
	case StateDiscarded:
		g.logf("%s reject seq=%d reason=already_discarded", op, seq)
		return ErrAlreadyDisc
	case StateQuarantined:
		return nil
	default:
		g.logf("%s reject seq=%d reason=not_quarantined", op, seq)
		return ErrNotQuarantin
	}
}

// adjudicate 依据三条规则裁决，不修改任何状态。
// 规则三即使在阻断规则未满足时也照常评估，所有未满足规则按次序列出。
func (g *Gate) adjudicate(b *batch) Decision {
	d := Decision{Seq: b.seq, Rows: b.rows, NullRows: b.nullRows, Violations: []RuleID{}}

	// 规则一（阻断）：行数低于下限。
	if b.rows < g.cfg.MinRows {
		d.Violations = append(d.Violations, RuleMinRows)
	}

	// 规则二（阻断）：空值行数/行数严格超过上限；行数为 0 不评估。交叉相乘比较。
	if b.rows > 0 && ratioGreater(b.nullRows, b.rows, g.cfg.MaxNullRate.Num, g.cfg.MaxNullRate.Den) {
		d.Violations = append(d.Violations, RuleNullRate)
	}

	// 规则三（告警）：仅当存在「序号更小且已放行」的基线批次时评估。
	med, hasBaseline := g.baselineMedian(b.seq)
	if hasBaseline {
		// rows < med*lo/100 或 rows > med*hi/100；恰等边界不算。
		loBoundNum := med * g.cfg.WarnLoPct
		hiBoundNum := med * g.cfg.WarnHiPct
		if b.rows*100 < loBoundNum || b.rows*100 > hiBoundNum {
			d.Violations = append(d.Violations, RuleBaseline)
		}
	}

	blocked := false
	for _, r := range d.Violations {
		if r == RuleMinRows || r == RuleNullRate {
			blocked = true
			break
		}
	}
	if blocked {
		d.Allowed = false
		d.Warning = false
		d.State = StateQuarantined
	} else {
		d.Allowed = true
		d.Warning = len(d.Violations) > 0 // 仅规则三可能残留
		if d.Warning {
			d.State = StateReleased
		} else {
			d.State = StateReleased
		}
	}
	return d
}

// commitDecision 落账一次提交/重投裁决：更新状态、基线集合、连续阻断计数与暂停标志。
func (g *Gate) commitDecision(b *batch, d Decision, op string) {
	if d.Allowed {
		b.state = StateReleased
		g.insertPassed(b.seq)
		g.blocks = 0 // 任何放行使连续阻断清零
		g.logf("%s seq=%d rows=%d null=%d -> ALLOW(warning=%v violations=%v) blocks=0 baseline_size=%d",
			op, b.seq, b.rows, b.nullRows, d.Warning, d.Violations, len(g.passed))
		return
	}

	b.state = StateQuarantined
	g.blocks++ // 重投裁决同样计入
	nowPaused := false
	if g.cfg.MaxBlocksM > 0 && g.blocks >= g.cfg.MaxBlocksM {
		g.paused = true
		nowPaused = true
	}
	g.logf("%s seq=%d rows=%d null=%d -> BLOCK(violations=%v) blocks=%d paused=%v",
		op, b.seq, b.rows, b.nullRows, d.Violations, g.blocks, nowPaused)
}

// insertPassed 按升序把序号加入已放行集合（去重）。
func (g *Gate) insertPassed(seq int64) {
	idx := sort.Search(len(g.passed), func(i int) bool { return g.passed[i] >= seq })
	if idx < len(g.passed) && g.passed[idx] == seq {
		return
	}
	g.passed = append(g.passed, 0)
	copy(g.passed[idx+1:], g.passed[idx:])
	g.passed[idx] = seq
}

// baselineMedian 返回序号严格小于 seq 的、序号最大的至多 K 个已放行批次
// 行数升序后的下标 (c-1)/2 值（c 为实际个数，整除，偶数个时取下中位数）。
func (g *Gate) baselineMedian(seq int64) (int64, bool) {
	idx := sort.Search(len(g.passed), func(i int) bool { return g.passed[i] >= seq })
	if idx == 0 {
		return 0, false
	}
	start := idx - g.cfg.BaselineK
	if start < 0 {
		start = 0
	}
	rows := make([]int64, 0, idx-start)
	for _, s := range g.passed[start:idx] {
		rows = append(rows, g.known[s].rows)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i] < rows[j] })
	c := len(rows)
	return rows[(c-1)/2], true
}

// ratioGreater 交叉相乘判断 a/b > p/q，使用大整数杜绝中间溢出。b、q 必须为正。
func ratioGreater(a, b, p, q int64) bool {
	lhs := new(big.Int).Mul(big.NewInt(a), big.NewInt(q))
	rhs := new(big.Int).Mul(big.NewInt(p), big.NewInt(b))
	return lhs.Cmp(rhs) > 0
}
