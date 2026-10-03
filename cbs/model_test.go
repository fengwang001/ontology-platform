package cbs_test

// 本文件实现一个按规则直写的朴素模拟器（model），并与 cbs.Ledger 对拍：
//   - Run 拆成逐单位执行，每单位后重判 q 是否归零；
//   - 零松弛判定用 big.Int 任意精度乘积；
//   - 带宽接纳用 big.Int 交叉相乘（通分）比较。
// 随机对拍 2000 组操作序列，日志打印每步的输入、输出与判定依据。

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"

	"ontology/cbs"
)

type modelServer struct {
	id    string
	Q     uint64
	P     uint64
	q     uint64
	d     uint64
	w     uint64
	woken bool
}

type model struct {
	now     uint64
	servers map[string]*modelServer
}

func newModel() *model {
	return &model{servers: map[string]*modelServer{}}
}

// modelState 用 big.Int 任意精度复核零松弛判定式 q·P >= (d-now)·Q。
func modelState(s *modelServer, now uint64) cbs.State {
	if s.w > 0 {
		return cbs.StateReady
	}
	if !s.woken {
		return cbs.StateReleased
	}
	lhs := new(big.Int).Mul(new(big.Int).SetUint64(s.q), new(big.Int).SetUint64(s.P))
	rhs := new(big.Int)
	if now < s.d {
		rhs.Mul(new(big.Int).SetUint64(s.d-now), new(big.Int).SetUint64(s.Q))
	}
	if lhs.Cmp(rhs) >= 0 {
		return cbs.StateReleased
	}
	return cbs.StateIdleOccupying
}

// modelAdmit 用 big.Int 交叉相乘判定 Σ占用 + Q/P <= 1：两边同乘
// D = cand.P · Π occ.P，比较分子，全程精确整数。
func (m *model) admit(now uint64, cand *modelServer) bool {
	var occ []*modelServer
	for _, s := range m.servers {
		if modelState(s, now) != cbs.StateReleased {
			occ = append(occ, s)
		}
	}
	den := new(big.Int).SetUint64(cand.P)
	for _, s := range occ {
		den.Mul(den, new(big.Int).SetUint64(s.P))
	}
	num := new(big.Int).SetUint64(cand.Q)
	for _, s := range occ {
		num.Mul(num, new(big.Int).SetUint64(s.P))
	}
	for _, s := range occ {
		term := new(big.Int).Div(new(big.Int).Set(den), new(big.Int).SetUint64(s.P))
		term.Mul(term, new(big.Int).SetUint64(s.Q))
		num.Add(num, term)
	}
	return num.Cmp(den) <= 0
}

func (m *model) earliestReady(now uint64) *modelServer {
	var best *modelServer
	for _, s := range m.servers {
		if modelState(s, now) != cbs.StateReady {
			continue
		}
		if best == nil || s.d < best.d || (s.d == best.d && s.id < best.id) {
			best = s
		}
	}
	return best
}

func (m *model) add(id string, Q, P uint64) error {
	if id == "" || Q < 1 || Q > P || P > cbs.MaxQP {
		return cbs.ErrInvalidParam
	}
	if _, ok := m.servers[id]; ok {
		return cbs.ErrDuplicateID
	}
	if len(m.servers) >= cbs.MaxServers {
		return cbs.ErrCapacityFull
	}
	m.servers[id] = &modelServer{id: id, Q: Q, P: P, q: Q}
	return nil
}

func (m *model) wake(id string, now, work uint64) error {
	if id == "" || work < 1 || work > cbs.MaxWork || now > cbs.MaxClock {
		return cbs.ErrInvalidParam
	}
	s, ok := m.servers[id]
	if !ok {
		return cbs.ErrNotFound
	}
	if now < m.now {
		return cbs.ErrClockRegression
	}
	switch modelState(s, now) {
	case cbs.StateReady:
		if s.w+work > cbs.MaxBacklog {
			return cbs.ErrInvalidParam
		}
		s.w += work
	case cbs.StateIdleOccupying:
		s.w = work
	default:
		if !m.admit(now, s) {
			return cbs.ErrInsufficientBandwidth
		}
		s.q = s.Q
		s.d = now + s.P
		s.w = work
		s.woken = true
	}
	m.now = now
	return nil
}

// run 朴素模拟：逐单位消耗，每单位后重判 q 是否归零并立即补充。
func (m *model) run(id string, now, delta uint64) error {
	if id == "" || delta < 1 || now > cbs.MaxClock {
		return cbs.ErrInvalidParam
	}
	s, ok := m.servers[id]
	if !ok {
		return cbs.ErrNotFound
	}
	if now < m.now {
		return cbs.ErrClockRegression
	}
	if modelState(s, now) != cbs.StateReady || delta > s.w {
		return cbs.ErrNotRunnable
	}
	if earliest := m.earliestReady(now); earliest != s {
		return cbs.ErrNotEarliestDeadline
	}
	for i := uint64(0); i < delta; i++ {
		s.q--
		if s.q == 0 {
			s.q = s.Q
			s.d += s.P
		}
	}
	s.w -= delta
	m.now = now + delta
	return nil
}

func (m *model) remove(id string) error {
	if id == "" {
		return cbs.ErrInvalidParam
	}
	s, ok := m.servers[id]
	if !ok {
		return cbs.ErrNotFound
	}
	if modelState(s, m.now) != cbs.StateReleased {
		return cbs.ErrBusy
	}
	delete(m.servers, id)
	return nil
}

func (m *model) total() *big.Rat {
	sum := new(big.Rat)
	for _, s := range m.servers {
		if modelState(s, m.now) == cbs.StateReleased {
			continue
		}
		sum.Add(sum, new(big.Rat).SetFrac64(int64(s.Q), int64(s.P)))
	}
	return sum
}

// --- 对拍驱动 ---

func describe(l *cbs.Ledger, id string) string {
	snap, err := l.State(id)
	if err != nil {
		return "<不存在>"
	}
	return fmt.Sprintf("{%v q=%d d=%d w=%d}", snap.State, snap.Remaining, snap.Deadline, snap.Backlog)
}

func checkErrMatch(t *testing.T, seq, op int, desc string, errL, errM error) {
	t.Helper()
	if (errL == nil) != (errM == nil) || (errL != nil && !sameReason(errL, errM)) {
		t.Fatalf("seq=%d op=%d %s: ledger err=%v, model err=%v", seq, op, desc, errL, errM)
	}
}

// sameReason 按 sentinel 比较拒绝原因（Ledger 的错误可能带上下文包装）。
func sameReason(a, b error) bool {
	for _, sentinel := range []error{
		cbs.ErrInvalidParam, cbs.ErrNotFound, cbs.ErrClockRegression,
		cbs.ErrInsufficientBandwidth, cbs.ErrNotRunnable,
		cbs.ErrNotEarliestDeadline, cbs.ErrBusy,
		cbs.ErrDuplicateID, cbs.ErrCapacityFull,
	} {
		if errors.Is(a, sentinel) || errors.Is(b, sentinel) {
			return errors.Is(a, sentinel) && errors.Is(b, sentinel)
		}
	}
	return false
}

// compareAll 在每一步操作后全量比对两个实现的可观测状态。
func compareAll(t *testing.T, seq, op int, l *cbs.Ledger, m *model, ids []string) {
	t.Helper()
	if l.Now() != m.now {
		t.Fatalf("seq=%d op=%d: clock ledger=%d model=%d", seq, op, l.Now(), m.now)
	}
	if got, want := l.Total().RatString(), m.total().RatString(); got != want {
		t.Fatalf("seq=%d op=%d: Total ledger=%s model=%s", seq, op, got, want)
	}
	idL, okL := l.Next()
	best := m.earliestReady(m.now)
	idM, okM := "", false
	if best != nil {
		idM, okM = best.id, true
	}
	if okL != okM || idL != idM {
		t.Fatalf("seq=%d op=%d: Next ledger=(%q,%v) model=(%q,%v)", seq, op, idL, okL, idM, okM)
	}
	for _, id := range ids {
		snap, errL := l.State(id)
		ms, okM := m.servers[id]
		if (errL == nil) != okM {
			t.Fatalf("seq=%d op=%d: State(%q) ledger err=%v, model 存在=%v", seq, op, id, errL, okM)
		}
		if errL != nil {
			continue
		}
		if snap.State != modelState(ms, m.now) || snap.Remaining != ms.q ||
			snap.Deadline != ms.d || snap.Backlog != ms.w {
			t.Fatalf("seq=%d op=%d: State(%q) ledger={%v q=%d d=%d w=%d} model={%v q=%d d=%d w=%d}",
				seq, op, id, snap.State, snap.Remaining, snap.Deadline, snap.Backlog,
				modelState(ms, m.now), ms.q, ms.d, ms.w)
		}
	}
}

// TestDifferentialRandom 对拍 2000 组随机操作序列；日志打印每步的
// 输入、输出与判定依据（目标服务器操作前状态）。
func TestDifferentialRandom(t *testing.T) {
	const sequences = 2000
	tally := map[error]int{}
	countErr := func(err error) {
		for _, s := range []error{
			cbs.ErrInvalidParam, cbs.ErrNotFound, cbs.ErrClockRegression,
			cbs.ErrInsufficientBandwidth, cbs.ErrNotRunnable,
			cbs.ErrNotEarliestDeadline, cbs.ErrBusy,
			cbs.ErrDuplicateID, cbs.ErrCapacityFull,
		} {
			if errors.Is(err, s) {
				tally[s]++
				return
			}
		}
	}
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewPCG(uint64(seq), 0xCB5))
		l := cbs.NewLedger()
		m := newModel()
		var ids []string

		// 初始注册 1~5 台服务器，Q、P 混合大小以覆盖多样带宽。
		nServers := 1 + r.IntN(5)
		for i := 0; i < nServers; i++ {
			id := fmt.Sprintf("s%d", i)
			var Q, P uint64
			switch r.IntN(10) {
			case 0, 1, 2, 3, 4: // 小参数，易产生交互
				P = 1 + uint64(r.IntN(12))
			case 5, 6, 7: // 中参数
				P = 1 + uint64(r.IntN(1000))
			default: // 大参数，逼近 1e6 上限
				P = 1 + uint64(r.IntN(1_000_000))
			}
			Q = 1 + uint64(r.IntN(int(P)))
			errL, errM := l.Add(id, Q, P), m.add(id, Q, P)
			checkErrMatch(t, seq, -1, fmt.Sprintf("Add(%s,%d,%d)", id, Q, P), errL, errM)
			countErr(errL)
			if errL == nil {
				ids = append(ids, id)
			}
		}
		compareAll(t, seq, 0, l, m, ids)

		for op := 0; op < 30; op++ {
			pickID := func() string {
				if len(ids) > 0 && r.IntN(10) < 8 {
					return ids[r.IntN(len(ids))]
				}
				return "ghost" // 不存在的编号
			}
			pickNow := func() uint64 {
				now := l.Now()
				switch r.IntN(20) {
				case 0:
					if now > 0 {
						return now - 1 // 时钟回退
					}
					return now
				case 1, 2:
					return now // 原地
				default:
					return now + uint64(r.IntN(12))
				}
			}
			switch r.IntN(100) {
			case 0, 1, 2, 3, 4: // Add：新增或重复
				var id string
				if r.IntN(2) == 0 && len(ids) > 0 {
					id = ids[r.IntN(len(ids))]
				} else {
					id = fmt.Sprintf("x%d", op)
				}
				P := 1 + uint64(r.IntN(20))
				Q := 1 + uint64(r.IntN(int(P)))
				desc := fmt.Sprintf("Add(%s,%d,%d)", id, Q, P)
				errL, errM := l.Add(id, Q, P), m.add(id, Q, P)
				checkErrMatch(t, seq, op, desc, errL, errM)
				countErr(errL)
				if errL == nil {
					ids = append(ids, id)
				}
				t.Logf("seq=%d op=%02d %s -> err=%v", seq, op, desc, errL)
			case 5, 6, 7, 8, 9, 10, 11, 12, 13, 14,
				15, 16, 17, 18, 19, 20, 21, 22, 23, 24,
				25, 26, 27, 28, 29, 30, 31, 32, 33, 34,
				35, 36, 37, 38, 39, 40, 41, 42, 43, 44: // Wake 40%
				id := pickID()
				now := pickNow()
				work := uint64(1 + r.IntN(20))
				if r.IntN(50) == 0 {
					work = 1_000_000_000 // 偶发大工作量
				}
				pre := describe(l, id)
				errL, errM := l.Wake(id, now, work), m.wake(id, now, work)
				desc := fmt.Sprintf("Wake(%s,now=%d,w=%d)", id, now, work)
				checkErrMatch(t, seq, op, desc, errL, errM)
				countErr(errL)
				t.Logf("seq=%d op=%02d %s pre=%s -> err=%v clock=%d total=%s",
					seq, op, desc, pre, errL, l.Now(), l.Total().RatString())
			case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54,
				55, 56, 57, 58, 59, 60, 61, 62, 63, 64,
				65, 66, 67, 68, 69: // Run 25%
				id := pickID()
				now := pickNow()
				delta := uint64(1 + r.IntN(25))
				pre := describe(l, id)
				errL, errM := l.Run(id, now, delta), m.run(id, now, delta)
				desc := fmt.Sprintf("Run(%s,now=%d,d=%d)", id, now, delta)
				checkErrMatch(t, seq, op, desc, errL, errM)
				countErr(errL)
				t.Logf("seq=%d op=%02d %s pre=%s -> err=%v clock=%d total=%s",
					seq, op, desc, pre, errL, l.Now(), l.Total().RatString())
			case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // Remove 10%
				id := pickID()
				pre := describe(l, id)
				errL, errM := l.Remove(id), m.remove(id)
				desc := fmt.Sprintf("Remove(%s)", id)
				checkErrMatch(t, seq, op, desc, errL, errM)
				countErr(errL)
				if errL == nil {
					next := ids[:0]
					for _, x := range ids {
						if x != id {
							next = append(next, x)
						}
					}
					ids = next
				}
				t.Logf("seq=%d op=%02d %s pre=%s -> err=%v", seq, op, desc, pre, errL)
			default: // 查询 20%：State/Total/Next 在 compareAll 中比对
				t.Logf("seq=%d op=%02d Query clock=%d total=%s",
					seq, op, l.Now(), l.Total().RatString())
			}
			compareAll(t, seq, op, l, m, ids)
		}
	}
	// 校验随机序列确实覆盖所有关键拒绝类别，并打印命中次数。
	for _, s := range []error{
		cbs.ErrNotFound, cbs.ErrClockRegression, cbs.ErrInsufficientBandwidth,
		cbs.ErrNotRunnable, cbs.ErrNotEarliestDeadline, cbs.ErrBusy,
		cbs.ErrDuplicateID,
	} {
		t.Logf("拒绝类别命中: %v -> %d 次", s, tally[s])
		if tally[s] == 0 {
			t.Fatalf("随机序列未覆盖拒绝类别: %v", s)
		}
	}
}
