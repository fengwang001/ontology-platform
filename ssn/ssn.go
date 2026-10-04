// Package ssn 实现建立在快照隔离（SI）之上的串行安全网（SSN）认证器。
//
// 认证器在提交时计算前驱高水位 η 与后继低水位 π，用排除窗口判据
// π <= η 拒绝可能造成非串行化的事务。提交序号、各版本上的水位与
// 中止原因均可精确复现：相同调用序列重放得到完全相同的结果。
package ssn

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// Inf 表示版本后继水位 ss 的 +∞ 初值。
const Inf = int64(math.MaxInt64)

// Reason 是可精确复现的拒绝 / 中止原因。
type Reason int

const (
	// ReasonNone 表示调用成功。
	ReasonNone Reason = iota
	// ReasonInvalidConfig 表示构造参数 K 或 H 越界，整体拒绝。
	ReasonInvalidConfig
	// ReasonUnknownTx 表示事务号不存在。
	ReasonUnknownTx
	// ReasonTxFinished 表示事务已结束（已提交或已中止）。
	ReasonTxFinished
	// ReasonKeyOutOfRange 表示键越界。
	ReasonKeyOutOfRange
	// ReasonSnapshotTooOld 表示快照过旧：链中最旧版本的 cs 已大于快照号。
	ReasonSnapshotTooOld
	// ReasonWriteWriteConflict 表示写写冲突：写集中某键最新版本的 cs 大于快照号。
	ReasonWriteWriteConflict
	// ReasonExclusionWindow 表示串行安全网排除窗口命中：π <= η。
	ReasonExclusionWindow
)

// String 返回原因的稳定文字描述。
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "ok"
	case ReasonInvalidConfig:
		return "invalid config"
	case ReasonUnknownTx:
		return "unknown transaction"
	case ReasonTxFinished:
		return "transaction already finished"
	case ReasonKeyOutOfRange:
		return "key out of range"
	case ReasonSnapshotTooOld:
		return "snapshot too old"
	case ReasonWriteWriteConflict:
		return "write-write conflict"
	case ReasonExclusionWindow:
		return "ssn exclusion window"
	default:
		return fmt.Sprintf("reason(%d)", int(r))
	}
}

// Error 描述一次被拒绝的调用。被拒绝的调用不改变任何状态。
type Error struct {
	Reason Reason
	Tx     int // 相关事务号；与事务无关时为 0
	Key    int // 相关键；与键无关时为 -1
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	switch {
	case e.Reason == ReasonInvalidConfig:
		return "ssn: invalid config"
	case e.Key >= 0:
		return fmt.Sprintf("ssn: tx %d key %d: %s", e.Tx, e.Key, e.Reason)
	case e.Tx > 0:
		return fmt.Sprintf("ssn: tx %d: %s", e.Tx, e.Reason)
	default:
		return fmt.Sprintf("ssn: %s", e.Reason)
	}
}

// HasReason 报告 err 是否为原因 r 的拒绝。
func HasReason(err error, r Reason) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason == r
	}
	return false
}

// version 是键版本链上的一个版本对象。被回收（截断）后只要仍有事务的
// 读集持有它，其水位仍随对象存在并参与认证。
type version struct {
	cs  int64 // 创建号（产生该版本的提交序号）
	ps  int64 // 读者高水位：读到该版本的已提交事务的最大提交号
	ss  int64 // 后继水位：覆盖者提交时的 π；未被覆盖时为 +Inf
	val int64
}

// transaction 是活跃或已结束事务的内部记录。
type transaction struct {
	snap   int64            // 快照号 s：Begin 时刻的全局提交计数 n
	done   bool             // 已提交或已中止
	reads  map[int]*version // 读集：键 -> 首次读到的版本对象
	writes map[int]int64    // 写缓冲：键 -> 缓冲值
}

// Certifier 是 SSN 认证器。所有方法可并发调用，效果等价于某个串行顺序。
type Certifier struct {
	mu     sync.Mutex
	k      int
	h      int
	n      int64 // 全局提交计数，初值 0
	nextTx int   // 已分配的事务号，初值 0（首个事务号为 1）

	chains [][]*version
	txs    map[int]*transaction

	// validateAccesses 统计最近一次 Commit 认证阶段（①②③）访问的
	// 版本数，用于验证其不超过读集与写集大小之和。非导出计数器。
	validateAccesses int
}

// New 构造认证器。K 为键数（1 到 64），H 为每键保留版本数（2 到 8）。
// 参数越界时以配置非法整体拒绝，返回 nil 与 *Error（ReasonInvalidConfig）。
func New(K, H int) (*Certifier, error) {
	if K < 1 || K > 64 || H < 2 || H > 8 {
		return nil, &Error{Reason: ReasonInvalidConfig, Key: -1}
	}
	c := &Certifier{
		k:      K,
		h:      H,
		chains: make([][]*version, K),
		txs:    make(map[int]*transaction),
	}
	for i := range c.chains {
		c.chains[i] = []*version{{cs: 0, ps: 0, ss: Inf, val: 0}}
	}
	return c, nil
}

// Begin 返回从 1 起递增的事务号，并记快照号 s 为当时的 n。
func (c *Certifier) Begin() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextTx++
	id := c.nextTx
	c.txs[id] = &transaction{
		snap:   c.n,
		reads:  make(map[int]*version),
		writes: make(map[int]int64),
	}
	return id
}

// Read 读取键 k。拒绝原因按顺序只报第一个：事务号不存在；事务已结束；
// 键越界；快照过旧（仅当必须读版本链时才判）。
func (c *Certifier) Read(t, k int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.txs[t]
	if !ok {
		return 0, &Error{Reason: ReasonUnknownTx, Tx: t, Key: -1}
	}
	if tx.done {
		return 0, &Error{Reason: ReasonTxFinished, Tx: t, Key: -1}
	}
	if k < 0 || k >= c.k {
		return 0, &Error{Reason: ReasonKeyOutOfRange, Tx: t, Key: k}
	}
	// 写过 k 则返回缓冲值；读过 k 则返回首次读到的值。均不判快照过旧。
	if v, ok := tx.writes[k]; ok {
		return v, nil
	}
	if v, ok := tx.reads[k]; ok {
		return v.val, nil
	}
	chain := c.chains[k]
	if chain[0].cs > tx.snap {
		return 0, &Error{Reason: ReasonSnapshotTooOld, Tx: t, Key: k}
	}
	// 读取 cs 不大于 s 的最新版本并记入读集（持有版本对象）。
	var v *version
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].cs <= tx.snap {
			v = chain[i]
			break
		}
	}
	tx.reads[k] = v
	return v.val, nil
}

// Write 把键 k 的值写入事务缓冲。拒绝原因顺序同 Read（无快照过旧项）。
func (c *Certifier) Write(t, k int, val int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.txs[t]
	if !ok {
		return &Error{Reason: ReasonUnknownTx, Tx: t, Key: -1}
	}
	if tx.done {
		return &Error{Reason: ReasonTxFinished, Tx: t, Key: -1}
	}
	if k < 0 || k >= c.k {
		return &Error{Reason: ReasonKeyOutOfRange, Tx: t, Key: k}
	}
	tx.writes[k] = val
	return nil
}

// Commit 依次执行：①写写冲突检查；②取候选提交号 c=n+1；③计算 η 与 π
// 并以排除窗口判据 π<=η 拒绝；④提交并更新水位。只读事务同样走②到④并
// 消耗提交号。两种中止原因可区分；中止后事务结束且不消耗提交号，n、
// 所有版本链与水位逐字段不变。
func (c *Certifier) Commit(t int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.txs[t]
	if !ok {
		return 0, &Error{Reason: ReasonUnknownTx, Tx: t, Key: -1}
	}
	if tx.done {
		return 0, &Error{Reason: ReasonTxFinished, Tx: t, Key: -1}
	}

	// 认证阶段（①②③）：访问的版本数不超过读集与写集大小之和。
	accesses := 0
	ww := false
	cand := c.n + 1 // ②候选提交号
	eta := int64(0)
	pi := cand
	for _, v := range tx.reads {
		accesses++
		if v.cs > eta {
			eta = v.cs
		}
		if v.ss < pi {
			pi = v.ss
		}
	}
	for k := range tx.writes {
		latest := c.chains[k][len(c.chains[k])-1]
		accesses++
		if latest.cs > tx.snap { // ①写写冲突
			ww = true
		}
		if latest.cs > eta {
			eta = latest.cs
		}
		if latest.ps > eta {
			eta = latest.ps
		}
	}
	c.validateAccesses = accesses

	if ww { // 写写冲突先于串行安全网上报
		tx.done = true
		return 0, &Error{Reason: ReasonWriteWriteConflict, Tx: t, Key: -1}
	}
	if pi <= eta { // ③排除窗口
		tx.done = true
		return 0, &Error{Reason: ReasonExclusionWindow, Tx: t, Key: -1}
	}

	// ④提交：推进 n，抬高读集版本 ps，写集键原最新版本 ss 置为 π，
	// 追加新版本并截断到 H 个。
	c.n = cand
	for _, v := range tx.reads {
		if cand > v.ps {
			v.ps = cand
		}
	}
	for k, val := range tx.writes {
		chain := c.chains[k]
		chain[len(chain)-1].ss = pi
		chain = append(chain, &version{cs: cand, ps: 0, ss: Inf, val: val})
		if len(chain) > c.h {
			chain = chain[len(chain)-c.h:]
		}
		c.chains[k] = chain
	}
	tx.done = true
	return cand, nil
}

// Abort 放弃活跃事务，不改任何版本。只检查事务号存在与未结束。
func (c *Certifier) Abort(t int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.txs[t]
	if !ok {
		return &Error{Reason: ReasonUnknownTx, Tx: t, Key: -1}
	}
	if tx.done {
		return &Error{Reason: ReasonTxFinished, Tx: t, Key: -1}
	}
	tx.done = true
	return nil
}

// VersionInfo 是单个版本的只读快照，用于精确复现水位。
type VersionInfo struct {
	CS    int64
	PS    int64
	SS    int64
	Value int64
}

// State 是认证器全部可观测状态（n 与各键版本链）的只读快照。
type State struct {
	N      int64
	Chains [][]VersionInfo
}

// Dump 返回当前全部可观测状态的只读快照。导出的观察方法不计入
// Commit 认证阶段的版本访问数。
func (c *Certifier) Dump() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := State{N: c.n, Chains: make([][]VersionInfo, len(c.chains))}
	for i, chain := range c.chains {
		vs := make([]VersionInfo, len(chain))
		for j, v := range chain {
			vs[j] = VersionInfo{CS: v.cs, PS: v.ps, SS: v.ss, Value: v.val}
		}
		s.Chains[i] = vs
	}
	return s
}
