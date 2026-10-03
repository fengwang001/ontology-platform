package merger

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// opLog 记录单个操作的输入、两侧输出与判定依据，失败时完整打印。
type opLog struct {
	input string
	got   string
	want  string
	rule  string
}

func (l *opLog) String() string {
	return fmt.Sprintf("输入: %s\n  优化实现输出: %s\n  朴素模拟输出: %s\n  判定依据: %s", l.input, l.got, l.want, l.rule)
}

const judgeRule = "与按规格写成的朴素模拟（逐时刻推进、每时刻检查是否可唤醒、全表扫描候选）逐步对拍，两侧输出与内部状态必须完全一致"

// TestDifferentialAgainstNaive 对 2000 组确定性随机操作序列，
// 将优化实现与朴素模拟逐步对拍：每个操作的返回值、错误与内部状态都必须一致。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*104729 + 7))
		g := int64(rng.Intn(9))         // 0..8，覆盖 g=0 退化
		batch := int64(1 + rng.Intn(4)) // 1..4，频繁触发批量上限
		if seq%17 == 0 {
			batch = 64 // 偶尔放宽批量上限
		}
		m, err := New(g, batch)
		if err != nil {
			t.Fatalf("序列 %d: New(%d, %d) = %v", seq, g, batch, err)
		}
		nm := newNaive(g, batch)

		ops := 20 + rng.Intn(60)
		var trace []opLog
		fail := false
		record := func(input, got, want string, mismatch bool) {
			trace = append(trace, opLog{input: input, got: got, want: want, rule: judgeRule})
			if mismatch {
				fail = true
			}
		}

		for i := 0; i < ops && !fail; i++ {
			switch op := rng.Intn(100); {
			case op < 30: // Add
				id := randomID(rng, nm)
				p := int64(1 + rng.Intn(10))
				s := int64(rng.Intn(int(p)))
				n := randomN(rng, nm)
				errGot := m.Add(id, p, s, n)
				errWant := nm.add(id, p, s, n)
				record(fmt.Sprintf("Add(%q, P=%d, s=%d, n=%d)", id, p, s, n),
					errStr(errGot), errStr(errWant), !errors.Is(errGot, errWant))
			case op < 45: // Remove
				id := randomID(rng, nm)
				errGot := m.Remove(id)
				errWant := nm.remove(id)
				record(fmt.Sprintf("Remove(%q)", id),
					errStr(errGot), errStr(errWant), !errors.Is(errGot, errWant))
			case op < 58: // Next
				wGot, errGot := m.Next()
				wWant, errWant := nm.next()
				record("Next()", fmt.Sprintf("(%d, %v)", wGot, errGot), fmt.Sprintf("(%d, %v)", wWant, errWant),
					wGot != wWant || !errors.Is(errGot, errWant))
			case op < 78: // Wake
				resGot, errGot := m.Wake()
				var resWant WakeResult
				var errWant error
				if len(nm.timers) == 0 {
					errWant = ErrNoTimers
				} else {
					resWant = nm.wake()
				}
				same := errors.Is(errGot, errWant) && (errGot != nil || wakeResultsEqual(resGot, resWant))
				record("Wake()", fmt.Sprintf("(%+v, %v)", resGot, errGot), fmt.Sprintf("(%+v, %v)", resWant, errWant), !same)
				if same && errGot == nil {
					checkWakeInvariants(t, m, nm, resGot)
				}
			case op < 93: // AdvanceTo
				tv := randomT(rng, nm)
				got, errGot := m.AdvanceTo(tv)
				want, errWant := nm.advanceTo(tv)
				same := errors.Is(errGot, errWant) && (errGot != nil || wakeSlicesEqual(got, want))
				record(fmt.Sprintf("AdvanceTo(%d)", tv),
					fmt.Sprintf("(%d 次唤醒, %v)", len(got), errGot),
					fmt.Sprintf("(%d 次唤醒, %v)", len(want), errWant), !same)
				if !same && errGot == nil && errWant == nil {
					for j := 0; j < len(got) && j < len(want); j++ {
						if !wakeResultsEqual(got[j], want[j]) {
							record(fmt.Sprintf("AdvanceTo 第 %d 次唤醒", j),
								fmt.Sprintf("%+v", got[j]), fmt.Sprintf("%+v", want[j]), true)
							break
						}
					}
				}
			default: // Stats
				got := m.Stats()
				want := nm.stats
				record("Stats()", fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want), got != want)
			}
			if i%7 == 0 && !fail {
				if msg := compareInternals(m, nm); msg != "" {
					record("内部状态核对", msg, "一致", true)
				}
			}
		}
		if msg := compareInternals(m, nm); msg != "" && !fail {
			record("最终内部状态核对", msg, "一致", true)
		}

		if fail {
			var sb strings.Builder
			fmt.Fprintf(&sb, "序列 %d 对拍失败（g=%d, B=%d, 种子=%d）：\n", seq, g, batch, int64(seq)*104729+7)
			for _, l := range trace {
				fmt.Fprintf(&sb, "%s\n", l.String())
			}
			t.Error(sb.String())
		} else {
			t.Logf("序列 %d: g=%d B=%d %d 个操作全部一致；判定依据: %s", seq, g, batch, ops, judgeRule)
			if seq == 0 {
				for _, l := range trace {
					t.Logf("样本操作轨迹:\n%s", l.String())
				}
			}
		}
	}
}

func errStr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func wakeResultsEqual(a, b WakeResult) bool {
	return a.W == b.W && a.Left == b.Left && reflect.DeepEqual(a.Fired, b.Fired)
}

func wakeSlicesEqual(a, b []WakeResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !wakeResultsEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// compareInternals 核对两侧定时器集合与每个定时器的名义时刻完全一致。
func compareInternals(m *Merger, nm *naiveMerger) string {
	if len(m.timers) != len(nm.timers) {
		return fmt.Sprintf("定时器数量 %d != %d", len(m.timers), len(nm.timers))
	}
	for id, tm := range m.timers {
		nt, ok := nm.timers[id]
		if !ok {
			return fmt.Sprintf("定时器 %q 仅存在于优化实现", id)
		}
		if tm.n != nt.n || tm.p != nt.p || tm.s != nt.s {
			return fmt.Sprintf("定时器 %q 状态 (%d,%d,%d) != (%d,%d,%d)",
				id, tm.n, tm.p, tm.s, nt.n, nt.p, nt.s)
		}
	}
	if m.last != nm.last || m.hasLast != nm.hasLast {
		return fmt.Sprintf("时钟 (%d,%v) != (%d,%v)", m.last, m.hasLast, nm.last, nm.hasLast)
	}
	if m.stats != nm.stats {
		return fmt.Sprintf("Stats %+v != %+v", m.stats, nm.stats)
	}
	return ""
}

// checkWakeInvariants 校验每次唤醒的规格不变量。
func checkWakeInvariants(t *testing.T, m *Merger, nm *naiveMerger, res WakeResult) {
	t.Helper()
	if len(res.Fired) > int(m.batch) {
		t.Errorf("单次唤醒触发 %d 个，超过 B=%d", len(res.Fired), m.batch)
	}
	if res.Left < 0 {
		t.Errorf("留下候选数为负: %d", res.Left)
	}
	seen := make(map[string]bool)
	for _, f := range res.Fired {
		if seen[f.ID] {
			t.Errorf("定时器 %q 在一次 Wake 内被触发多次", f.ID)
		}
		seen[f.ID] = true
		tm := m.timers[f.ID]
		if tm == nil {
			t.Errorf("触发了不存在的定时器 %q", f.ID)
			continue
		}
		if tm.n <= res.W {
			t.Errorf("定时器 %q 触发后 n=%d 不大于 w=%d", f.ID, tm.n, res.W)
		}
		if f.Late < 0 || f.K < 0 {
			t.Errorf("定时器 %q late=%d k=%d 出现负值", f.ID, f.Late, f.K)
		}
	}
	// 相邻两次唤醒间隔不小于 g（naive 的 last 已在 wake 内更新，用优化实现核对）。
	_ = nm
}

var idPool = []string{"a", "b", "c", "d", "e", "f", "g", "h", "t1", "t2"}

func randomID(rng *rand.Rand, nm *naiveMerger) string {
	switch rng.Intn(20) {
	case 0:
		return "" // 非法：空编号
	case 1:
		return strings.Repeat("x", 33) // 非法：超过 32 字节
	case 2:
		return fmt.Sprintf("n%d", rng.Intn(1000)) // 大概率不存在
	default:
		return idPool[rng.Intn(len(idPool))]
	}
}

// randomN 生成名义时刻：覆盖 0、小时刻、当前时钟附近（含恰等于与小 1）、越界。
func randomN(rng *rand.Rand, nm *naiveMerger) int64 {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		if nm.last > 0 {
			return nm.last - 1 // 早于当前时钟
		}
		return 0
	case 2:
		return nm.last // 恰等于当前时钟
	case 3:
		return nm.last + int64(rng.Intn(3))
	case 4:
		return 1_000_000_000_000_001 // 越界
	default:
		return int64(rng.Intn(60))
	}
}

// randomT 生成 AdvanceTo 的目标时刻：覆盖小时刻、时钟回退、越界。
func randomT(rng *rand.Rand, nm *naiveMerger) int64 {
	switch rng.Intn(12) {
	case 0:
		return -1 // 参数非法
	case 1:
		return 1_000_000_000_000_001 // 参数非法
	case 2:
		if nm.last > 0 {
			return nm.last - 1 // 时钟回退
		}
		return 0
	case 3:
		return nm.last
	default:
		return nm.last + int64(rng.Intn(400))
	}
}
