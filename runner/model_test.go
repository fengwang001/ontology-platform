package runner_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/ledger"
	"ontology/runner"
	"ontology/script"
)

// rejKind 是拒绝原因的类别，用于引擎与朴素模拟之间的对拍。
type rejKind int

const (
	kOK rejKind = iota
	kArgument
	kPermission
	kNowRegression
	kFailed
	kChecksum
	kOutOfOrder
	kNoFailed
	kNoUndo
	kUnknown
)

func (k rejKind) String() string {
	switch k {
	case kOK:
		return "OK"
	case kArgument:
		return "Argument"
	case kPermission:
		return "Permission"
	case kNowRegression:
		return "NowRegression"
	case kFailed:
		return "Failed"
	case kChecksum:
		return "Checksum"
	case kOutOfOrder:
		return "OutOfOrder"
	case kNoFailed:
		return "NoFailed"
	case kNoUndo:
		return "NoUndo"
	}
	return "Unknown"
}

// classify 把引擎返回的 error 映射为 (rejKind, ver)。
func classify(err error) (rejKind, uint32) {
	if err == nil {
		return kOK, 0
	}
	var rj *runner.Reject
	if !errors.As(err, &rj) {
		return kUnknown, 0
	}
	switch {
	case errors.Is(err, runner.ErrArgument):
		return kArgument, rj.Ver
	case errors.Is(err, runner.ErrPermission):
		return kPermission, rj.Ver
	case errors.Is(err, runner.ErrNowRegression):
		return kNowRegression, rj.Ver
	case errors.Is(err, runner.ErrFailed):
		return kFailed, rj.Ver
	case errors.Is(err, runner.ErrChecksum):
		return kChecksum, rj.Ver
	case errors.Is(err, runner.ErrOutOfOrder):
		return kOutOfOrder, rj.Ver
	case errors.Is(err, runner.ErrNoFailed):
		return kNoFailed, rj.Ver
	case errors.Is(err, runner.ErrNoUndo):
		return kNoUndo, rj.Ver
	}
	return kUnknown, 0
}

var errInjected = errors.New("injected callback failure")

// failPlan 记录回调全局序号的失败/panic 注入计划；引擎回调与模型各自计数、查同一份计划。
type failPlan struct {
	engN    int
	failAt  map[int]bool
	panicAt map[int]bool
}

func (p *failPlan) engineCb(ver uint32) error {
	p.engN++
	if p.panicAt[p.engN] {
		panic("injected panic")
	}
	if p.failAt[p.engN] {
		return errInjected
	}
	return nil
}

// model 是按规则逐步写成的朴素模拟，与引擎实现对拍。
type model struct {
	ooo      bool
	lim      int
	reg      map[uint32]script.Script
	rows     []ledger.Row
	nextRank uint64
	maxNow   uint64
	callN    int
	plan     *failPlan
}

// cbOutcome 模拟一次回调：推进模型自己的计数并查同一份注入计划。
func (m *model) cbOutcome() bool {
	m.callN++
	return m.plan.failAt[m.callN] || m.plan.panicAt[m.callN]
}

func validRole(role int) bool { return role >= 0 && role <= 2 }

func (m *model) register(role int, s script.Script) (rejKind, uint32) {
	if !s.Valid() || !validRole(role) {
		return kArgument, 0
	}
	if role < 1 {
		return kPermission, 0
	}
	m.reg[s.Ver] = s
	return kOK, 0
}

func (m *model) successRows() map[uint32]ledger.Row {
	out := map[uint32]ledger.Row{}
	for _, r := range m.rows {
		if r.Status == ledger.StatusSuccess {
			out[r.Ver] = r
		}
	}
	return out
}

func (m *model) appendRow(ver uint32, st ledger.Status) {
	m.rows = append(m.rows, ledger.Row{Rank: m.nextRank, Ver: ver, Sum: m.reg[ver].Sum, Status: st})
	m.nextRank++
}

func (m *model) migrate(role int, now uint64) (applied []uint32, failed uint32, more bool, k rejKind, ver uint32) {
	if !validRole(role) || now > 1_000_000_000_000 {
		return nil, 0, false, kArgument, 0
	}
	if role < 1 {
		return nil, 0, false, kPermission, 0
	}
	if now < m.maxNow {
		return nil, 0, false, kNowRegression, 0
	}
	for _, r := range m.rows {
		if r.Status == ledger.StatusFailed {
			return nil, 0, false, kFailed, r.Ver
		}
	}
	srows := m.successRows()
	vers := sortedKeys(srows)
	for _, v := range vers {
		if m.reg[v].Sum != srows[v].Sum {
			return nil, 0, false, kChecksum, v
		}
	}
	if !m.ooo {
		var maxA uint32
		for _, v := range vers {
			if v > maxA {
				maxA = v
			}
		}
		var best uint32
		for v := range m.reg {
			if _, ok := srows[v]; v < maxA && !ok && (best == 0 || v < best) {
				best = v
			}
		}
		if best != 0 {
			return nil, 0, false, kOutOfOrder, best
		}
	}
	m.maxNow = now
	var pending []uint32
	for v := range m.reg {
		if _, ok := srows[v]; !ok {
			pending = append(pending, v)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	applied = []uint32{}
	for _, v := range pending {
		if len(applied) >= m.lim {
			return applied, 0, true, kOK, 0
		}
		if m.cbOutcome() {
			m.appendRow(v, ledger.StatusFailed)
			return applied, v, false, kOK, 0
		}
		m.appendRow(v, ledger.StatusSuccess)
		applied = append(applied, v)
	}
	return applied, 0, false, kOK, 0
}

func (m *model) repair(role int, now uint64) (int, rejKind, uint32) {
	if !validRole(role) || now > 1_000_000_000_000 {
		return 0, kArgument, 0
	}
	if role != 2 {
		return 0, kPermission, 0
	}
	if now < m.maxNow {
		return 0, kNowRegression, 0
	}
	hasF := false
	for _, r := range m.rows {
		if r.Status == ledger.StatusFailed {
			hasF = true
			break
		}
	}
	if !hasF {
		return 0, kNoFailed, 0
	}
	m.maxNow = now
	kept := m.rows[:0]
	removed := 0
	for _, r := range m.rows {
		if r.Status == ledger.StatusFailed {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	m.rows = kept
	return removed, kOK, 0
}

func (m *model) undo(role int, now uint64, to uint32) (undone []uint32, failed uint32, k rejKind, ver uint32) {
	if !validRole(role) || now > 1_000_000_000_000 || to > 1_000_000 {
		return nil, 0, kArgument, 0
	}
	if role != 2 {
		return nil, 0, kPermission, 0
	}
	if now < m.maxNow {
		return nil, 0, kNowRegression, 0
	}
	for _, r := range m.rows {
		if r.Status == ledger.StatusFailed {
			return nil, 0, kFailed, r.Ver
		}
	}
	var sel []ledger.Row
	for _, r := range m.rows {
		if r.Status == ledger.StatusSuccess && r.Ver > to {
			sel = append(sel, r)
		}
	}
	var maxNoUndo uint32
	for _, r := range sel {
		if !m.reg[r.Ver].HasUndo && r.Ver > maxNoUndo {
			maxNoUndo = r.Ver
		}
	}
	if maxNoUndo != 0 {
		return nil, 0, kNoUndo, maxNoUndo
	}
	m.maxNow = now
	sort.Slice(sel, func(i, j int) bool { return sel[i].Rank > sel[j].Rank })
	undone = []uint32{}
	for _, r := range sel {
		if m.cbOutcome() {
			m.appendRow(r.Ver, ledger.StatusFailed)
			return undone, r.Ver, kOK, 0
		}
		for i := range m.rows {
			if m.rows[i].Rank == r.Rank {
				m.rows[i].Status = ledger.StatusUndone
			}
		}
		undone = append(undone, r.Ver)
	}
	return undone, 0, kOK, 0
}

func sortedKeys(m map[uint32]ledger.Row) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *model) registered() []script.Script {
	out := make([]script.Script, 0, len(m.reg))
	for _, s := range m.reg {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ver < out[j].Ver })
	return out
}

func equalRows(a, b []ledger.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalScripts(a, b []script.Script) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalU32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkInvariants 校验账本不变量：rank 严格递增、同一 ver 至多一条 S 行。
func checkInvariants(t *testing.T, rows []ledger.Row) {
	t.Helper()
	var prev uint64
	sCount := map[uint32]int{}
	for _, r := range rows {
		if r.Rank <= prev {
			t.Fatalf("rank not strictly increasing: %+v", rows)
		}
		prev = r.Rank
		if r.Status == ledger.StatusSuccess {
			sCount[r.Ver]++
			if sCount[r.Ver] > 1 {
				t.Fatalf("duplicate S row for ver=%d: %+v", r.Ver, rows)
			}
		}
	}
}

// TestModelRandom 用 2000 组随机操作序列（回调失败位置随机注入）与朴素模拟对拍。
// 每个操作都在日志中打印输入、输出与判定依据（-v 可见）。
func TestModelRandom(t *testing.T) {
	const sequences = 2000
	tally := map[rejKind]int{}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		plan := &failPlan{failAt: map[int]bool{}, panicAt: map[int]bool{}}
		for i := 1; i <= 400; i++ {
			switch x := rng.Intn(100); {
			case x < 6:
				plan.failAt[i] = true
			case x < 8:
				plan.panicAt[i] = true
			}
		}
		ooo := rng.Intn(2) == 0
		lim := 1 + rng.Intn(6)
		eng, err := runner.New(ooo, lim, plan.engineCb, plan.engineCb)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		m := &model{ooo: ooo, lim: lim, reg: map[uint32]script.Script{}, nextRank: 1, plan: plan}
		now := uint64(0)
		ops := 10 + rng.Intn(25)
		for op := 0; op < ops; op++ {
			switch rng.Intn(10) {
			case 0:
				// now 不变
			case 1:
				if now > 0 {
					now-- // 制造 now 回退
				}
			case 2:
				now = 1_000_000_000_001 // 非法 now
			default:
				if now > 1_000_000_000_000 {
					now = m.maxNow + uint64(rng.Intn(3)) // 非法 now 用后拉回合法区间
				} else {
					now += uint64(rng.Intn(3))
				}
			}
			ctx := func(format string, args ...any) string {
				prefix := "seq=" + itoa(seq) + " op=" + itoa(op) + " ooo=" + boolStr(ooo) +
					" lim=" + itoa(lim) + " now=" + itoa64(now) + " "
				return prefix + fmt.Sprintf(format, args...)
			}
			switch rng.Intn(4) {
			case 0:
				role := []int{-1, 0, 1, 1, 2, 2, 3}[rng.Intn(7)]
				s := script.Script{Ver: uint32(1 + rng.Intn(8)), Sum: uint64(rng.Intn(4)), HasUndo: rng.Intn(2) == 0}
				if rng.Intn(25) == 0 {
					s.Ver = 0 // 非法脚本
				}
				engErr := eng.Register(role, s)
				mk, mv := m.register(role, s)
				ek, ev := classify(engErr)
				t.Logf("%s", ctx("Register(role=%d, %+v) engine=%v model=%s(ver=%d)", role, s, engErr, mk, mv))
				tally[mk]++
				if ek != mk || ev != mv {
					t.Fatalf("%s", ctx("Register mismatch: engine=(%s,%d) model=(%s,%d)", ek, ev, mk, mv))
				}
			case 1:
				role := []int{-1, 0, 1, 1, 2, 2, 3}[rng.Intn(7)]
				applied, failed, more, engErr := eng.Migrate(role, now)
				ma, mf, mm, mk, mv := m.migrate(role, now)
				ek, ev := classify(engErr)
				t.Logf("%s", ctx("Migrate(role=%d) engine=(applied=%v failed=%d more=%v err=%v) model=(applied=%v failed=%d more=%v %s ver=%d)",
					role, applied, failed, more, engErr, ma, mf, mm, mk, mv))
				tally[mk]++
				if ek != mk || ev != mv || failed != mf || more != mm || !equalU32(applied, ma) {
					t.Fatalf("%s", ctx("Migrate mismatch: engine=(%v,%d,%v,%s,%d) model=(%v,%d,%v,%s,%d)",
						applied, failed, more, ek, ev, ma, mf, mm, mk, mv))
				}
			case 2:
				role := []int{-1, 0, 1, 1, 2, 2, 3}[rng.Intn(7)]
				n, engErr := eng.Repair(role, now)
				mn, mk, mv := m.repair(role, now)
				ek, ev := classify(engErr)
				t.Logf("%s", ctx("Repair(role=%d) engine=(n=%d err=%v) model=(n=%d %s ver=%d)", role, n, engErr, mn, mk, mv))
				tally[mk]++
				if ek != mk || ev != mv || n != mn {
					t.Fatalf("%s", ctx("Repair mismatch: engine=(%d,%s,%d) model=(%d,%s,%d)", n, ek, ev, mn, mk, mv))
				}
			case 3:
				role := []int{-1, 0, 1, 1, 2, 2, 3}[rng.Intn(7)]
				to := uint32(rng.Intn(10))
				if rng.Intn(25) == 0 {
					to = 1_000_001 // 非法 to
				}
				undone, failed, engErr := eng.Undo(role, now, to)
				mu, mf, mk, mv := m.undo(role, now, to)
				ek, ev := classify(engErr)
				t.Logf("%s", ctx("Undo(role=%d, to=%d) engine=(undone=%v failed=%d err=%v) model=(undone=%v failed=%d %s ver=%d)",
					role, to, undone, failed, engErr, mu, mf, mk, mv))
				tally[mk]++
				if ek != mk || ev != mv || failed != mf || !equalU32(undone, mu) {
					t.Fatalf("%s", ctx("Undo mismatch: engine=(%v,%d,%s,%d) model=(%v,%d,%s,%d)",
						undone, failed, ek, ev, mu, mf, mk, mv))
				}
			}
			if plan.engN != m.callN {
				t.Fatalf("%s", ctx("callback counter desync: engine=%d model=%d", plan.engN, m.callN))
			}
			if got, want := eng.Ledger(), m.rows; !equalRows(got, want) {
				t.Fatalf("%s", ctx("ledger mismatch:\n got=%+v\nwant=%+v", got, want))
			}
			if got, want := eng.Registered(), m.registered(); !equalScripts(got, want) {
				t.Fatalf("%s", ctx("registry mismatch:\n got=%+v\nwant=%+v", got, want))
			}
			checkInvariants(t, eng.Ledger())
		}
	}
	t.Logf("reject/accept distribution over %d sequences: %v", sequences, tally)
}

func itoa(v int) string {
	return itoa64(uint64(v))
}

func itoa64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestConcurrentSerializable 并发调用下不变量仍成立（配合 -race）。
func TestConcurrentSerializable(t *testing.T) {
	eng, err := runner.New(true, 3, okCb, okCb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var nowCounter atomic.Uint64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ver := uint32((g*100+i)%30 + 1)
				switch i % 5 {
				case 0:
					_ = eng.Register(1, script.Script{Ver: ver, Sum: uint64(ver), HasUndo: true})
				case 1:
					_, _, _, _ = eng.Migrate(1, nowCounter.Add(1))
				case 2:
					_, _, _ = eng.Undo(2, nowCounter.Add(1), uint32(g))
				case 3:
					_, _ = eng.Repair(2, nowCounter.Add(1))
				default:
					_ = eng.Ledger()
					_ = eng.Registered()
				}
			}
		}(g)
	}
	wg.Wait()
	rows := eng.Ledger()
	checkInvariants(t, rows)
	// A 与账本一致：每个 S 行的 ver 都在登记表中。
	reg := map[uint32]bool{}
	for _, s := range eng.Registered() {
		reg[s.Ver] = true
	}
	for _, r := range rows {
		if r.Status == ledger.StatusSuccess && !reg[r.Ver] {
			t.Fatalf("S row ver=%d not registered: %+v", r.Ver, rows)
		}
	}
}
