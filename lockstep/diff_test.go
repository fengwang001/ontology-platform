package lockstep

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/turn"
)

// naive 是逐毫秒推进的朴素参考模型：
// 每个时钟滴答先判断 now==dl 的超时结算并连锁，再暴露给操作判定。
type naive struct {
	n, a, r, kd int
	t           int64
	cur         int
	dl          int64
	maxNow      int64
	stored      map[[2]int][]byte // [k,p] -> cmd（含全部玩家，判定到齐时只看活跃）
	active      []bool
	miss        []int
	last        map[int][]byte
	log         []turn.Record
}

func newNaive(n int, t int64, a, r, kd int, now0 int64) *naive {
	m := &naive{
		n: n, t: t, a: a, r: r, kd: kd,
		cur: 1, dl: now0 + t, maxNow: now0,
		stored: make(map[[2]int][]byte),
		active: make([]bool, n),
		miss:   make([]int, n),
		last:   make(map[int][]byte),
	}
	for p := 0; p < n; p++ {
		m.active[p] = true
	}
	return m
}

func (m *naive) settle(ts, newDL int64) turn.Record {
	rec := turn.Record{Turn: m.cur, TS: ts, Slots: make([]turn.Slot, m.n)}
	for p := 0; p < m.n; p++ {
		key := [2]int{m.cur, p}
		if cmd, ok := m.stored[key]; ok {
			cp := append([]byte(nil), cmd...)
			rec.Slots[p] = turn.Slot{Cmd: cp, Source: turn.Live}
			m.miss[p] = 0
			m.last[p] = cp
		} else {
			m.miss[p]++
			if m.miss[p] <= m.r {
				if last, has := m.last[p]; has {
					rec.Slots[p] = turn.Slot{Cmd: append([]byte(nil), last...), Source: turn.Repeat}
				} else {
					rec.Slots[p] = turn.Slot{Source: turn.Blank}
				}
			} else {
				rec.Slots[p] = turn.Slot{Source: turn.Blank}
			}
			if m.active[p] && m.miss[p] >= m.kd {
				m.active[p] = false
			}
		}
		delete(m.stored, key)
	}
	m.cur++
	m.dl = newDL
	m.log = append(m.log, rec)
	return rec
}

func (m *naive) ready() bool {
	for p := 0; p < m.n; p++ {
		if m.active[p] {
			if _, ok := m.stored[[2]int{m.cur, p}]; !ok {
				return false
			}
		}
	}
	activeN := 0
	for p := range m.active {
		if m.active[p] {
			activeN++
		}
	}
	return activeN > 0
}

// catchup 是入口处理的朴素实现。截止时刻相邻恰好相距 T、连锁在同一时刻发生，
// 所以“逐毫秒推进、在每个 now==dl 的滴答结算并连锁”与“按逻辑 dl 循环结算”
// 逐事件等价（前者只是空转到下一个截止时刻）；这里按逻辑 dl 结算，
// now>=dl（取等超时）即触发，结算时刻取 dl 而非被发现的 now。
func (m *naive) catchup(now int64) {
	for now >= m.dl {
		ts := m.dl
		m.settle(ts, ts+m.t)
		for m.ready() {
			m.settle(ts, ts+m.t)
		}
	}
}

func (m *naive) submit(now int64, p, k int, cmd []byte) error {
	if p < 0 || p >= m.n || k < 1 || len(cmd) < 1 || len(cmd) > 64 ||
		now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if now < m.maxNow {
		return ErrClockRewind
	}
	// 合法时钟观测：即使随后因迟到/超前/重复被拒，入口处理与时钟下限仍生效，
	// 以保证结算时刻非降；被拒的只是该操作的输入。
	m.maxNow = now
	m.catchup(now)
	if k < m.cur {
		return ErrLate
	}
	if k > m.cur+m.a {
		return ErrAhead
	}
	if _, ok := m.stored[[2]int{k, p}]; ok {
		return ErrDuplicate
	}
	m.stored[[2]int{k, p}] = append([]byte(nil), cmd...)
	m.active[p] = true
	for m.ready() {
		m.settle(now, now+m.t)
	}
	return nil
}

func (m *naive) advance(now int64) ([]turn.Record, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	if now < m.maxNow {
		return nil, ErrClockRewind
	}
	before := len(m.log)
	m.catchup(now)
	m.maxNow = now
	out := make([]turn.Record, len(m.log)-before)
	copy(out, m.log[before:])
	return out, nil
}

func recordsEqual(a, b []turn.Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Turn != b[i].Turn || a[i].TS != b[i].TS || len(a[i].Slots) != len(b[i].Slots) {
			return false
		}
		for p := range a[i].Slots {
			x, y := a[i].Slots[p], b[i].Slots[p]
			if x.Source != y.Source || string(x.Cmd) != string(y.Cmd) {
				return false
			}
		}
	}
	return true
}

// TestRandomAgainstNaive：小参数下 1000 组随机操作，引擎与逐毫秒朴素模型逐字段对照。
func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1000
	rng := rand.New(rand.NewSource(20261004))
	for g := 0; g < groups; g++ {
		n := 1 + rng.Intn(3)
		var tt int64 = int64(1 + rng.Intn(4))
		a := rng.Intn(3)
		r := rng.Intn(3)
		kd := 1 + rng.Intn(3)
		var now0 int64 = int64(rng.Intn(5))

		fast := New(n, tt, a, r, kd, now0)
		ref := newNaive(n, tt, a, r, kd, now0)

		var trace []string
		trace = append(trace, fmt.Sprintf("组%d 参数 N=%d T=%d A=%d R=%d Kd=%d now0=%d",
			g, n, tt, a, r, kd, now0))

		steps := 20 + rng.Intn(40)
		for s := 0; s < steps; s++ {
			now := ref.maxNow + int64(rng.Intn(int(tt)*3+1))
			if rng.Intn(10) == 0 {
				now = ref.maxNow // 相等时刻也允许
			}
			if rng.Intn(10) == 0 && ref.maxNow > now0 { // 少量故意回退
				now = ref.maxNow - int64(1+rng.Intn(2))
			}

			if rng.Intn(5) == 0 {
				recs1, err1 := fast.Advance(now)
				recs2, err2 := ref.advance(now)
				trace = append(trace, fmt.Sprintf("Advance(%d) => fastErr=%v 朴素Err=%v 新结算=%d",
					now, err1, err2, len(recs1)))
				if !errors.Is(err1, err2) {
					t.Fatalf("%s\nAdvance 错误不一致 fast=%v naive=%v", dump(trace), err1, err2)
				}
				if err1 == nil && !recordsEqual(recs1, recs2) {
					t.Fatalf("%s\nAdvance 新记录不一致\nfast=%s\nnaive=%s",
						dump(trace), dumpRecs(recs1), dumpRecs(recs2))
				}
			} else {
				p := rng.Intn(n)
				// 回合号：多数围绕 cur，少量故意越界。
				k := ref.cur - 1 + rng.Intn(a+3)
				if k < 1 {
					k = 1
				}
				cmdLen := 1 + rng.Intn(3)
				cmd := []byte{byte('a' + rng.Intn(26))}
				for c := 1; c < cmdLen; c++ {
					cmd = append(cmd, byte('0'+rng.Intn(10)))
				}
				var rawCmd []byte
				if rng.Intn(15) == 0 { // 少量非法 cmd
					if rng.Intn(2) == 0 {
						rawCmd = nil
					} else {
						rawCmd = make([]byte, 65)
					}
				} else {
					rawCmd = cmd
				}
				err1 := fast.Submit(now, p, k, rawCmd)
				err2 := ref.submit(now, p, k, rawCmd)
				trace = append(trace, fmt.Sprintf("Submit(%d,p%d,k%d,%q) => fast=%v 朴素=%v；判定=%s | cur(fast=%d,naive=%d)",
					now, p, k, string(rawCmd), err1, err2, verdict(err1), fast.Cur(), ref.cur))
				if !errors.Is(err1, err2) {
					t.Fatalf("%s\nSubmit 错误不一致 fast=%v naive=%v", dump(trace), err1, err2)
				}
			}

			// 每次操作后全量对照：日志、cur、dl、活跃、m。
			if fast.Cur() != ref.cur || fast.Deadline() != ref.dl {
				t.Fatalf("%s\ncur/dl 不一致 fast=(%d,%d) naive=(%d,%d)",
					dump(trace), fast.Cur(), fast.Deadline(), ref.cur, ref.dl)
			}
			lf, lr := fast.Log(1), append([]turn.Record(nil), ref.log...)
			if !recordsEqual(lf, lr) {
				t.Fatalf("%s\n全量日志不一致\nfast=%s\nnaive=%s", dump(trace), dumpRecs(lf), dumpRecs(lr))
			}
			// 显式不变量：回合连续各一次、时刻非降、相邻结算时刻之差 ∈ [0,T]、每回合恰 N 条。
			for i, rec := range lf {
				if rec.Turn != i+1 || len(rec.Slots) != n {
					t.Fatalf("%s\n记录结构非法: %+v", dump(trace), rec)
				}
				if i > 0 {
					d := rec.TS - lf[i-1].TS
					if d < 0 || d > tt {
						t.Fatalf("%s\n结算时刻差 %d 超出 [0,T=%d]", dump(trace), d, tt)
					}
				}
			}
			for p := 0; p < n; p++ {
				if fast.Active(p) != ref.active[p] || fast.Miss(p) != ref.miss[p] {
					t.Fatalf("%s\n玩家%d 状态不一致 fast(active=%v,m=%d) naive(active=%v,m=%d)",
						dump(trace), p, fast.Active(p), fast.Miss(p), ref.active[p], ref.miss[p])
				}
			}
		}

		if g < 5 || g == groups-1 {
			t.Logf("\n%s\n最终结算 %d 回合：\n%s", trace[0], len(fast.Log(1)), dumpRecs(fast.Log(1)))
		}
	}
}

func dump(trace []string) string {
	out := ""
	for _, line := range trace {
		out += "\n  " + line
	}
	return out
}

func dumpRecs(recs []turn.Record) string {
	out := ""
	for _, r := range recs {
		out += fmt.Sprintf("\n  回合%d ts=%d %s", r.Turn, r.TS, slotsString(r.Slots))
	}
	return out
}
