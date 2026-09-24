// Package api 是对外门面：参数校验、批量追加、按时间戳查位点、自检。
// 依赖 tlog。
package api

import (
	"errors"

	"ontology/tlog"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrBadBase    = errors.New("api: negative base")
	ErrBadMaxMsgs = errors.New("api: non-positive maxMsgs")
	ErrNegativeTS = tlog.ErrNegativeTS
	ErrCapacity   = tlog.ErrCapacity
)

// API 是按时间戳查位点的对外入口，可被多 goroutine 并发使用。
type API struct {
	lg *tlog.Log
}

// New 构造实例；base 为负或 maxMsgs 非正时返回可判定错误。
func New(base int64, maxMsgs int) (*API, error) {
	if base < 0 {
		return nil, ErrBadBase
	}
	if maxMsgs <= 0 {
		return nil, ErrBadMaxMsgs
	}
	return &API{lg: tlog.New(base, maxMsgs)}, nil
}

// Append 批量追加；任一条被拒则整批不生效。成功返回本批首条位点。
func (a *API) Append(ts []int64) (first int64, err error) {
	return a.lg.Append(ts)
}

// Lookup 返回位点最小的、TS >= t 的消息位点；没有则 (LEO, false)。
func (a *API) Lookup(t int64) (off int64, found bool) {
	return a.lg.Lookup(t)
}

// LEO 返回日志结束位点。
func (a *API) LEO() int64 { return a.lg.LEO() }

// naive 是朴素参照：从 base 起逐条扫描，第一条 TS >= t 即停。
func naive(base int64, ts []int64, t int64) (int64, bool) {
	for i, v := range ts {
		if v >= t {
			return base + int64(i), true
		}
	}
	return base + int64(len(ts)), false
}

// SelfCheck 对一组内置追加序列核验四条不变量，全部通过才返回 true。
func (a *API) SelfCheck() bool {
	seqs := [][]int64{
		{50, 40, 70, 60, 70, 65, 90, 80, 90, 85},
		{5},
		{0, 0, 0, 1, 0, 2, 2, 1, 3},
		lcgSeq(200, 97), lcgSeq(999, 31),
	}
	for _, seq := range seqs {
		lg := tlog.New(100, len(seq)+1)
		if _, err := lg.Append(seq); err != nil {
			return false
		}
		lo, hi := int64(0), int64(0)
		for i, v := range seq {
			if i == 0 || v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		snap := lg.Snapshot()
		prev := int64(-1)
		for t := lo - 1; t <= hi+1; t++ { // 不变量 1 与 3
			gotOff, gotFound := lg.Lookup(t)
			wantOff, wantFound := naive(100, snap, t)
			if gotOff != wantOff || gotFound != wantFound {
				return false
			}
			if prev >= 0 && gotOff < prev {
				return false
			}
			prev = gotOff
		}
		var runMax int64 // 不变量 2：索引严格单调且等于迄今最大
		for i, e := range lg.Index() {
			if i > 0 && e.TS <= runMax {
				return false
			}
			for _, v := range snap[:e.Off-100+1] {
				if v > runMax {
					runMax = v
				}
			}
			if e.TS != runMax {
				return false
			}
		}
	}
	// 不变量 4：失败不留痕
	lg := tlog.New(7, 4)
	lg.Append([]int64{3, 9})
	leo, idx, snap := lg.LEO(), lg.Index(), lg.Snapshot()
	if _, err := lg.Append([]int64{1, -1}); !errors.Is(err, tlog.ErrNegativeTS) {
		return false
	}
	if _, err := lg.Append([]int64{1, 2, 3}); !errors.Is(err, tlog.ErrCapacity) {
		return false
	}
	if lg.LEO() != leo || len(lg.Index()) != len(idx) || len(lg.Snapshot()) != len(snap) {
		return false
	}
	if _, err := lg.Append([]int64{4}); err != nil || lg.LEO() != 10 {
		return false // 被拒后仍可正常使用
	}
	return true
}

// lcgSeq 生成确定性的非单调时间戳序列（线性同余，免外部依赖）。
func lcgSeq(n int, mod int64) []int64 {
	out := make([]int64, n)
	x := int64(42)
	for i := range out {
		x = (x*6364136223846793005 + 1442695040888963407) >> 16
		if x < 0 {
			x = -x
		}
		out[i] = x % mod
	}
	return out
}
