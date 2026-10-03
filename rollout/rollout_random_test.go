package rollout

import (
	"errors"
	"math/bits"
	"math/rand"
	"sort"
	"testing"
)

// naive 是按规格逐条写成的朴素模拟，与 Reviewer 实现相互独立：
// 闸门比较使用 math/bits 的 128 位乘法而非 math/big。
type naive struct {
	cfg       Config
	cums      []int64
	state     State
	batch     int
	st        int64
	rn, en    int64
	rb, eb    int64
	ps, fs    int64
	rollbacks int64
	maxNow    int64
}

func newNaive(cfg Config) *naive {
	var cums []int64
	for _, p := range cfg.P {
		cum := (cfg.N*p + 99) / 100
		if len(cums) == 0 || cums[len(cums)-1] != cum {
			cums = append(cums, cum)
		}
	}
	return &naive{cfg: cfg, cums: cums, state: StateIdle}
}

// start 返回 (拒绝原因, 是否被接受)。
func (s *naive) start(now int64) (Reason, bool) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ReasonInvalidParam, false
	}
	if s.state != StateIdle {
		return ReasonInvalidState, false
	}
	if now < s.maxNow {
		return ReasonClockRollback, false
	}
	s.state = StatePublishing
	s.batch = 0
	s.st = now
	s.maxNow = now
	return 0, true
}

// observe 返回 (判定, 拒绝原因, 是否被接受)。
func (s *naive) observe(now int64, d [4]int64) (Verdict, Reason, bool) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return 0, ReasonInvalidParam, false
	}
	for _, v := range d {
		if v < 0 || v > 1_000_000_000 {
			return 0, ReasonInvalidParam, false
		}
	}
	if d[1] > d[0] || d[3] > d[2] {
		return 0, ReasonInvalidParam, false
	}
	if s.rn+d[0] > 1_000_000_000 || s.en+d[1] > 1_000_000_000 ||
		s.rb+d[2] > 1_000_000_000 || s.eb+d[3] > 1_000_000_000 {
		return 0, ReasonInvalidParam, false
	}
	if s.state != StatePublishing {
		return 0, ReasonInvalidState, false
	}
	if now < s.maxNow {
		return 0, ReasonClockRollback, false
	}
	s.rn += d[0]
	s.en += d[1]
	s.rb += d[2]
	s.eb += d[3]
	s.maxNow = now
	if now < s.st+s.cfg.S {
		return VerdictSoaking, 0, true
	}
	if s.rn < s.cfg.Nmin {
		return VerdictInsufficientSample, 0, true
	}
	if s.en >= s.cfg.Ef && gateFail128(s.en, s.rb, s.eb, s.rn, s.cfg.Tol) {
		s.fs++
		s.ps = 0
		if s.fs >= s.cfg.R {
			s.rollbacks++
			if s.batch == 0 {
				s.state = StateAborted
			} else {
				s.batch--
				s.st = now + s.cfg.H
				s.rn, s.en, s.rb, s.eb, s.ps, s.fs = 0, 0, 0, 0, 0, 0
				if s.rollbacks >= s.cfg.M {
					s.state = StateFrozen
				}
			}
		}
		return VerdictFail, 0, true
	}
	s.ps++
	if s.ps >= s.cfg.K {
		if s.batch == len(s.cums)-1 {
			s.state = StateDone
		} else {
			s.batch++
			s.st = now
			s.rn, s.en, s.rb, s.eb, s.ps, s.fs = 0, 0, 0, 0, 0, 0
		}
	}
	return VerdictPass, 0, true
}

// gateFail128 用 128 位无符号乘法比较 en×rb×100 与 eb×rn×(100+tol)。
func gateFail128(en, rb, eb, rn, tol int64) bool {
	lHi, lLo := bits.Mul64(uint64(en)*uint64(rb), 100)
	rHi, rLo := bits.Mul64(uint64(eb)*uint64(rn), uint64(100+tol))
	return lHi > rHi || (lHi == rHi && lLo > rLo)
}

func (s *naive) status() Status {
	st := Status{
		State:     s.state,
		Batch:     s.batch,
		St:        s.st,
		Rn:        s.rn,
		En:        s.en,
		Rb:        s.rb,
		Eb:        s.eb,
		Ps:        s.ps,
		Fs:        s.fs,
		Rollbacks: s.rollbacks,
	}
	if s.state == StateAborted {
		st.Instances = 0
	} else {
		st.Instances = s.cums[s.batch]
	}
	return st
}

type randOp struct {
	isStart bool
	now     int64
	d       [4]int64
}

func randomConfig(rng *rand.Rand) Config {
	var n int64
	switch rng.Intn(3) {
	case 0:
		n = int64(1 + rng.Intn(20)) // 小 N，易触发 cum 去重
	case 1:
		n = int64(1 + rng.Intn(1000))
	default:
		n = int64(1 + rng.Intn(1_000_000))
	}
	m := 1 + rng.Intn(8)
	seen := map[int64]bool{}
	p := make([]int64, 0, m)
	for len(p) < m-1 {
		v := int64(1 + rng.Intn(99))
		if seen[v] {
			continue
		}
		seen[v] = true
		p = append(p, v)
	}
	sort.Slice(p, func(i, j int) bool { return p[i] < p[j] })
	p = append(p, 100)
	return Config{
		N:    n,
		P:    p,
		S:    int64(rng.Intn(21)),
		K:    int64(1 + rng.Intn(4)),
		Tol:  int64(rng.Intn(1001)),
		Ef:   int64(rng.Intn(6)),
		Nmin: int64(rng.Intn(201)),
		R:    int64(1 + rng.Intn(3)),
		H:    int64(rng.Intn(21)),
		M:    int64(1 + rng.Intn(3)),
	}
}

func randomOps(rng *rand.Rand) []randOp {
	numOps := 30 + rng.Intn(50)
	ops := make([]randOp, 0, numOps)
	base := int64(0)
	for i := 0; i < numOps; i++ {
		var o randOp
		o.isStart = (i == 0 && rng.Intn(10) != 0) || rng.Intn(100) < 3
		base += int64(rng.Intn(25))
		o.now = base
		switch rng.Intn(25) {
		case 0:
			o.now = base - int64(1+rng.Intn(10)) // 可能时钟回退或为负
		case 1:
			o.now = 1_000_000_000_000_001 // now 越界
		case 2:
			o.now = -int64(1 + rng.Intn(5)) // now 为负
		}
		switch rng.Intn(20) {
		case 0: // 非法：错误数大于请求数
			o.d[0] = int64(rng.Intn(50))
			o.d[1] = o.d[0] + 1 + int64(rng.Intn(10))
			o.d[2] = int64(rng.Intn(50))
			o.d[3] = int64(rng.Intn(51))
			if o.d[3] > o.d[2] {
				o.d[3] = o.d[2]
			}
		case 1: // 非法：增量越界或为负
			for j := range o.d {
				o.d[j] = int64(rng.Intn(50))
			}
			if rng.Intn(2) == 0 {
				o.d[rng.Intn(4)] = -1
			} else {
				o.d[rng.Intn(4)] = 1_000_000_001
			}
		case 2: // 大增量：易触发累计越界与大数闸门
			o.d[0] = 1_000_000_000
			o.d[2] = 1_000_000_000
			o.d[1] = int64(rng.Intn(2)) * 1_000_000_000
			o.d[3] = int64(rng.Intn(2)) * 1_000_000_000
		default: // 常规小增量
			o.d[0] = int64(rng.Intn(60))
			o.d[1] = int64(rng.Intn(int(o.d[0]) + 1))
			o.d[2] = int64(rng.Intn(60))
			o.d[3] = int64(rng.Intn(int(o.d[2]) + 1))
		}
		ops = append(ops, o)
	}
	return ops
}

// classify 将 err 分类为 (拒绝原因, 是否被接受)；未知错误类型直接判测试失败。
func classify(t *testing.T, err error) (Reason, bool) {
	t.Helper()
	if err == nil {
		return 0, true
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("非 RejectError 类型: %v", err)
	}
	switch re.Reason {
	case ReasonInvalidParam, ReasonInvalidState, ReasonClockRollback:
	default:
		t.Fatalf("未知拒绝原因: %v", err)
	}
	return re.Reason, false
}

const noVerdict = Verdict(-1)

// TestRandomAgainstNaive 对 2000 组随机操作序列，逐 op 对比 Reviewer 与朴素模拟
// 的返回与完整状态，并用第二个 Reviewer 重放验证完全可复现。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	var seenDone, seenAborted, seenFrozen, seenRollback int
	var seenReject [3]int
	var seenVerdict [4]int
	for seq := 0; seq < 2000; seq++ {
		cfg := randomConfig(rng)
		ops := randomOps(rng)
		rv1, err := New(cfg)
		if err != nil {
			t.Fatalf("序列 %d: New(%+v) 被误拒: %v", seq, cfg, err)
		}
		rv2, err := New(cfg)
		if err != nil {
			t.Fatalf("序列 %d: New(%+v) 被误拒: %v", seq, cfg, err)
		}
		nv := newNaive(cfg)
		t.Logf("序列 %d 输入: N=%d P=%v S=%d K=%d tol=%d Ef=%d Nmin=%d R=%d H=%d M=%d",
			seq, cfg.N, cfg.P, cfg.S, cfg.K, cfg.Tol, cfg.Ef, cfg.Nmin, cfg.R, cfg.H, cfg.M)
		for i, o := range ops {
			var v1, v2, vn Verdict = noVerdict, noVerdict, noVerdict
			var e1, e2 error
			var nReason Reason
			var nOK bool
			if o.isStart {
				e1 = rv1.Start(o.now)
				e2 = rv2.Start(o.now)
				nReason, nOK = nv.start(o.now)
			} else {
				v1, e1 = rv1.Observe(o.now, o.d[0], o.d[1], o.d[2], o.d[3])
				v2, e2 = rv2.Observe(o.now, o.d[0], o.d[1], o.d[2], o.d[3])
				vn, nReason, nOK = nv.observe(o.now, o.d)
			}
			r1, ok1 := classify(t, e1)
			r2, ok2 := classify(t, e2)
			if !ok1 {
				seenReject[int(r1)]++
			} else if !o.isStart {
				seenVerdict[int(v1)]++
			}
			if ok1 != nOK || (!ok1 && r1 != nReason) {
				t.Fatalf("序列 %d op %d %+v: Reviewer=(接受=%v,原因=%s) 朴素=(接受=%v,原因=%s)",
					seq, i, o, ok1, r1, nOK, nReason)
			}
			if v1 != vn || v1 != v2 {
				t.Fatalf("序列 %d op %d %+v: 判定 v1=%s v2=%s 朴素=%s", seq, i, o, v1, v2, vn)
			}
			if ok1 != ok2 || (!ok1 && r1 != r2) {
				t.Fatalf("序列 %d op %d: 两次重放拒绝不一致 e1=%v e2=%v", seq, i, e1, e2)
			}
			s1, s2, sn := rv1.Status(), rv2.Status(), nv.status()
			if s1 != s2 {
				t.Fatalf("序列 %d op %d: 重放状态不一致 %+v vs %+v", seq, i, s1, s2)
			}
			if s1 != sn {
				t.Fatalf("序列 %d op %d %+v: 状态不一致 Reviewer=%+v 朴素=%+v", seq, i, o, s1, sn)
			}
			t.Logf("序列 %d op %d 输入=%+v 输出=(判定=%s,错误=%v) 依据: state=%s batch=%d inst=%d st=%d (rn,en,rb,eb)=(%d,%d,%d,%d) ps=%d fs=%d rb次=%d",
				seq, i, o, v1, e1, s1.State, s1.Batch, s1.Instances, s1.St,
				s1.Rn, s1.En, s1.Rb, s1.Eb, s1.Ps, s1.Fs, s1.Rollbacks)
		}
		switch nv.state {
		case StateDone:
			seenDone++
		case StateAborted:
			seenAborted++
		case StateFrozen:
			seenFrozen++
		}
		if nv.rollbacks > 0 {
			seenRollback++
		}
	}
	t.Logf("覆盖统计: 完成=%d 中止=%d 冻结=%d 有回退=%d 拒绝[参数=%d 状态=%d 时钟=%d] 判定[浸泡=%d 样本不足=%d 通过=%d 失败=%d]",
		seenDone, seenAborted, seenFrozen, seenRollback,
		seenReject[0], seenReject[1], seenReject[2],
		seenVerdict[0], seenVerdict[1], seenVerdict[2], seenVerdict[3])
	if seenDone == 0 || seenAborted == 0 || seenFrozen == 0 || seenRollback == 0 {
		t.Fatalf("随机序列未覆盖全部终态: 完成=%d 中止=%d 冻结=%d 有回退=%d", seenDone, seenAborted, seenFrozen, seenRollback)
	}
	for i, n := range seenReject {
		if n == 0 {
			t.Fatalf("随机序列未覆盖拒绝原因 %d", i)
		}
	}
	for i, n := range seenVerdict {
		if n == 0 {
			t.Fatalf("随机序列未覆盖判定 %d", i)
		}
	}
}
