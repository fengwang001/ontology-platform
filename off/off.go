// Package off 分区位点状态：已提交位点、检查点、恢复逻辑、单调性校验。
// 依赖 arc。所有方法先校验后写状态，校验失败不留任何痕迹。
package off

import (
	"errors"
	"math"

	"ontology/arc"
)

// 可判定哨兵错误，互不相同。
var (
	ErrNonMonotonic  = errors.New("off: commit not monotonic (off not increasing or ts regressed)")
	ErrNowRegression = errors.New("off: evict now regressed")
	ErrNoCommit      = errors.New("off: no committed offset to checkpoint")
)

// NegInf 表示「无位点」的负无穷。
const NegInf = int64(math.MinInt64)

// Partition 单分区状态。
type Partition struct {
	committed int64
	hasCommit bool
	lastTs    int64
	cp        int64
	hasCp     bool
	lastNow   int64
	hasNow    bool
	ar        arc.Archive
}

// Commit 令已提交位点 C=off 并归档 (off, ts)。
// 要求 off > 当前 C 且 ts 不小于上次 ts，否则整体失败、状态不变。
func (p *Partition) Commit(off, ts int64) error {
	if p.hasCommit && (off <= p.committed || ts < p.lastTs) {
		return ErrNonMonotonic
	}
	p.committed, p.hasCommit, p.lastTs = off, true, ts
	p.ar.Append(off, ts)
	return nil
}

// Checkpoint 令检查点 cp=C；须已有提交。检查点永不驱逐。
func (p *Partition) Checkpoint() error {
	if !p.hasCommit {
		return ErrNoCommit
	}
	p.cp, p.hasCp = p.committed, true
	return nil
}

// Evict 驱逐 ts < now-retention 的归档条目；now 不得小于上次 now。
func (p *Partition) Evict(now, retention int64) error {
	if p.hasNow && now < p.lastNow {
		return ErrNowRegression
	}
	p.lastNow, p.hasNow = now, true
	p.ar.Evict(now - retention)
	return nil
}

// Committed 返回当前已提交位点。
func (p *Partition) Committed() (int64, bool) { return p.committed, p.hasCommit }

// Recover 恢复位点 = max(cp, 幸存归档最大位点)；两者皆无返回 NegInf。
// 与「取 cp、扫描幸存归档取最大、两者取大」的批量重算逐字一致。
func (p *Partition) Recover() int64 {
	r := NegInf
	if p.hasCp {
		r = p.cp
	}
	if off, ok := p.ar.Max(); ok && off > r {
		r = off
	}
	return r
}
