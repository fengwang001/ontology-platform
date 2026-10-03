// Package sequential 实现带样本比例校验、按有效检视次数换用边界表的
// A/B 序贯检验停止判定器。全部判定使用整数（大整数）运算，结果可精确复现。
package sequential

import (
	"errors"
	"math/big"
	"sync"
)

// Conclusion 表示一次检视或当前的结论。
type Conclusion int

const (
	// Continue 继续试验（样本不足、仅观察或未达任何停止边界）。
	Continue Conclusion = iota
	// Win B 组显著优于 A 组（D > 0），试验停止。
	Win
	// Lose B 组显著差于 A 组（D < 0），试验停止。
	Lose
	// Futile 达到样本上限或穿越无效边界，试验停止。
	Futile
	// Imbalance 实际分流比偏离预期超过容差，试验停止。
	Imbalance
)

func (c Conclusion) String() string {
	switch c {
	case Continue:
		return "继续"
	case Win:
		return "胜出"
	case Lose:
		return "变差"
	case Futile:
		return "无效"
	case Imbalance:
		return "比例异常"
	}
	return "未知"
}

// 拒绝原因，按此优先级只报第一个：参数非法 > 已停止 > 数据回退。
var (
	// ErrInvalidParam 构造参数越界，或 Look 数值不满足 0 <= c <= n <= 1e6。
	ErrInvalidParam = errors.New("sequential: 参数非法")
	// ErrStopped 状态为已停止时的 Look。
	ErrStopped = errors.New("sequential: 已停止")
	// ErrRegression 四个数中任一个小于上一次被接受的检视。
	ErrRegression = errors.New("sequential: 数据回退")
)

const (
	maxGroupN   = 1_000_000
	maxBound    = 1_000_000
	maxNmax     = 2_000_000
	maxTableLen = 8
)

// Judge 是序贯检验停止判定器，可并发调用。
type Judge struct {
	mu sync.Mutex

	rA, rB  int64
	nmin    int64
	minStep int64
	nmax    int64
	table   []int64
	tf      int64
	tau     int64

	stopped      bool
	looks        int
	lastCountedN int64
	lastConcl    Conclusion

	lastNA, lastCA, lastNB, lastCB int64
}

// Status 是 Judge 状态的快照。
type Status struct {
	Stopped        bool       // 是否已停止
	Looks          int        // 有效检视次数（仅进入显著性判定的检视计数）
	LastCountedN   int64      // 上次计数检视时的总样本量 n
	LastConclusion Conclusion // 最近一次结论
}

// NewJudge 构造判定器。
//
//	rA, rB：预期分流比，各为 1..100
//	nmin：最小组样本，1..1e6
//	minStep：有效检视最小增量，1..1e6
//	nmax：样本上限，2..2e6
//	table：停止边界表 T_1..T_k（100*z^2 下限），长度 1..8，各为 1..1e6
//	tf：无效边界，0..1e6
//	tau：比例容差百分数，0..100
func NewJudge(rA, rB, nmin, minStep, nmax int64, table []int64, tf, tau int64) (*Judge, error) {
	if rA < 1 || rA > 100 || rB < 1 || rB > 100 {
		return nil, ErrInvalidParam
	}
	if nmin < 1 || nmin > maxGroupN {
		return nil, ErrInvalidParam
	}
	if minStep < 1 || minStep > maxGroupN {
		return nil, ErrInvalidParam
	}
	if nmax < 2 || nmax > maxNmax {
		return nil, ErrInvalidParam
	}
	if len(table) < 1 || len(table) > maxTableLen {
		return nil, ErrInvalidParam
	}
	for _, t := range table {
		if t < 1 || t > maxBound {
			return nil, ErrInvalidParam
		}
	}
	if tf < 0 || tf > maxBound {
		return nil, ErrInvalidParam
	}
	if tau < 0 || tau > 100 {
		return nil, ErrInvalidParam
	}
	tbl := make([]int64, len(table))
	copy(tbl, table)
	return &Judge{
		rA: rA, rB: rB,
		nmin: nmin, minStep: minStep, nmax: nmax,
		table: tbl, tf: tf, tau: tau,
	}, nil
}

// Look 提交 A、B 两组的累计样本数与累计转化数，返回本次结论。
// 被拒绝时不改变任何内部状态，并返回区分原因的 error。
func (j *Judge) Look(nA, cA, nB, cB int64) (Conclusion, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// 1. 参数非法
	if nA < 0 || nA > maxGroupN || cA < 0 || cA > nA ||
		nB < 0 || nB > maxGroupN || cB < 0 || cB > nB {
		return j.lastConcl, ErrInvalidParam
	}
	// 2. 已停止
	if j.stopped {
		return j.lastConcl, ErrStopped
	}
	// 3. 数据回退
	if nA < j.lastNA || cA < j.lastCA || nB < j.lastNB || cB < j.lastCB {
		return j.lastConcl, ErrRegression
	}

	// 接受本次检视：不论结论都更新上一次被接受的数值。
	j.lastNA, j.lastCA, j.lastNB, j.lastCB = nA, cA, nB, cB

	n := nA + nB
	c := cA + cB
	d := cB*nA - cA*nB

	// （一）比例异常：n >= 2*nmin 时检查，不计数。
	if n >= 2*j.nmin {
		dev := nA*j.rB - nB*j.rA
		if dev < 0 {
			dev = -dev
		}
		if dev*100 > j.tau*(nA*j.rB+nB*j.rA) {
			return j.stop(Imbalance), nil
		}
	}
	// （二）样本不足，不计数。
	if nA < j.nmin || nB < j.nmin {
		return j.conclude(Continue), nil
	}
	// （三）增量不足，仅观察，不计数。
	if n-j.lastCountedN < j.minStep {
		return j.conclude(Continue), nil
	}

	// （四）有效检视：计数并换用边界 T_min(i,k)。
	j.looks++
	j.lastCountedN = n
	idx := j.looks
	if idx > len(j.table) {
		idx = len(j.table)
	}
	t := j.table[idx-1]

	var significant, futileStat bool
	if c == 0 || c == n {
		// z^2 未定义，按 z^2=0 处理：显著必为否；
		// 无效边界的“小于”当且仅当 Tf>0 时成立。
		significant = false
		futileStat = j.tf > 0
	} else {
		// lhs = D^2 * n * 100；rhs(T) = T * nA * nB * c * (n-c)。
		lhs := new(big.Int).SetInt64(d)
		lhs.Mul(lhs, lhs)
		lhs.Mul(lhs, big.NewInt(n))
		lhs.Mul(lhs, big.NewInt(100))
		rhs := new(big.Int).SetInt64(nA)
		rhs.Mul(rhs, big.NewInt(nB))
		rhs.Mul(rhs, big.NewInt(c))
		rhs.Mul(rhs, big.NewInt(n-c))
		rhsSig := new(big.Int).Mul(big.NewInt(t), rhs)
		significant = lhs.Cmp(rhsSig) >= 0
		rhsFut := new(big.Int).Mul(big.NewInt(j.tf), rhs)
		futileStat = lhs.Cmp(rhsFut) < 0
	}

	if significant {
		if d > 0 {
			return j.stop(Win), nil
		}
		return j.stop(Lose), nil
	}
	if n >= j.nmax {
		return j.stop(Futile), nil
	}
	if 2*n >= j.nmax && futileStat {
		return j.stop(Futile), nil
	}
	return j.conclude(Continue), nil
}

// conclude 记录非停止结论并返回。
func (j *Judge) conclude(c Conclusion) Conclusion {
	j.lastConcl = c
	return c
}

// stop 记录停止结论、置状态为已停止并返回。
func (j *Judge) stop(c Conclusion) Conclusion {
	j.stopped = true
	j.lastConcl = c
	return c
}

// Status 返回当前状态快照。
func (j *Judge) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Status{
		Stopped:        j.stopped,
		Looks:          j.looks,
		LastCountedN:   j.lastCountedN,
		LastConclusion: j.lastConcl,
	}
}
