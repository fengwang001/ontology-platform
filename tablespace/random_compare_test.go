package tablespace

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func errName(err error) string {
	switch err {
	case nil:
		return "OK"
	case ErrInvalidArgument:
		return "INVALID_ARGUMENT"
	case ErrNoSuchSegment:
		return "NO_SUCH_SEGMENT"
	case ErrPageNotOwned:
		return "PAGE_NOT_OWNED"
	case ErrNoSpace:
		return "NO_SPACE"
	default:
		return "UNKNOWN"
	}
}

func isNaiveError(reason string) bool {
	switch reason {
	case "INVALID_ARGUMENT", "NO_SUCH_SEGMENT", "PAGE_NOT_OWNED", "NO_SPACE":
		return true
	}
	return false
}

// implQueue 读取实现中段 s 的非满队列（链表顺序）。
func implQueue(a *Allocator, s int) []int {
	var q []int
	idx := a.segs[s].headExt
	for idx != -1 {
		q = append(q, idx)
		idx = a.extents[idx].next
	}
	return q
}

// checkInvariants 校验题面全部结构不变量。
func checkInvariants(t *testing.T, a *Allocator, where string) {
	t.Helper()
	totalUsed := 0
	freePages := 0
	segUsedCounted := make([]int, len(a.segs))
	queueSeen := make(map[[2]int]bool)

	for s := 1; s < len(a.segs); s++ {
		if !a.segs[s].alive {
			if a.segs[s].headExt != -1 || a.segs[s].tailExt != -1 {
				t.Fatalf("%s: dead segment %d still has queue", where, s)
			}
			continue
		}
		for _, idx := range implQueue(a, s) {
			key := [2]int{s, idx}
			if queueSeen[key] {
				t.Fatalf("%s: extent %d duplicated in segment %d queue", where, idx, s)
			}
			queueSeen[key] = true
			ex := &a.extents[idx]
			if ex.state != 3 || ex.owner != s {
				t.Fatalf("%s: queue extent %d is not SEG(%d): state=%d owner=%d", where, idx, s, ex.state, ex.owner)
			}
			if ex.used == a.x {
				t.Fatalf("%s: full extent %d stays in segment %d queue", where, idx, s)
			}
		}
	}

	for idx := range a.extents {
		ex := &a.extents[idx]
		switch ex.state {
		case 0:
			if ex.used != 0 {
				t.Fatalf("%s: FREE extent %d used=%d", where, idx, ex.used)
			}
		case 1:
			if ex.used < 1 || ex.used >= a.x {
				t.Fatalf("%s: FRAG extent %d used=%d out of [1,%d)", where, idx, ex.used, a.x)
			}
		case 2:
			if ex.used != a.x {
				t.Fatalf("%s: FULLFRAG extent %d used=%d, want %d", where, idx, ex.used, a.x)
			}
		case 3:
			if ex.owner <= 0 || !a.segs[ex.owner].alive {
				t.Fatalf("%s: SEG extent %d owned by invalid segment %d", where, idx, ex.owner)
			}
			if ex.used < 1 || ex.used > a.x {
				t.Fatalf("%s: SEG extent %d used=%d", where, idx, ex.used)
			}
			if !queueSeen[[2]int{ex.owner, idx}] && ex.used < a.x {
				t.Fatalf("%s: non-full SEG extent %d missing from segment %d queue", where, idx, ex.owner)
			}
		}

		usedHere := 0
		for off := 0; off < a.x; off++ {
			owner, _ := a.PageOwner(idx*a.x + off)
			if owner == 0 {
				freePages++
				continue
			}
			usedHere++
			if owner < 0 || owner >= len(a.segs) || !a.segs[owner].alive {
				t.Fatalf("%s: page %d owned by invalid segment %d", where, idx*a.x+off, owner)
			}
			if ex.state == 3 && owner != ex.owner {
				t.Fatalf("%s: SEG extent %d page %d owner %d != %d", where, idx, idx*a.x+off, owner, ex.owner)
			}
			segUsedCounted[owner]++
		}
		if usedHere != ex.used {
			t.Fatalf("%s: extent %d used=%d but counted %d", where, idx, ex.used, usedHere)
		}
		totalUsed += ex.used
	}

	for s := 1; s < len(a.segs); s++ {
		if a.segs[s].alive && segUsedCounted[s] != a.segs[s].used {
			t.Fatalf("%s: segment %d bookkeeping used=%d, counted=%d",
				where, s, a.segs[s].used, segUsedCounted[s])
		}
	}
	if totalUsed+freePages != a.e*a.x {
		t.Fatalf("%s: used %d + free %d != %d", where, totalUsed, freePages, a.e*a.x)
	}
}

// TestRandomAgainstNaive 用 2000 组随机操作序列与朴素模型逐步对照，
// 打印输入、输出与判定依据；失败时转储完整操作日志。
func TestRandomAgainstNaive(t *testing.T) {
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		rng := rand.New(rand.NewPCG(uint64(tc+1)*1000003, uint64(tc+1)*7+13))
		x := 2 + rng.IntN(8) // 2..9
		f := 1 + rng.IntN(x) // 1..x
		e := 1 + rng.IntN(7) // 1..7

		a, err := New(x, f, e)
		if err != nil {
			t.Fatalf("case %d: New(%d,%d,%d): %v", tc, x, f, e, err)
		}
		m := newNaive(x, f, e)

		var logBuf []string
		logBuf = append(logBuf, fmt.Sprintf("case %d: X=%d F=%d E=%d", tc, x, f, e))
		failf := func(format string, args ...any) {
			for _, line := range logBuf {
				t.Log(line)
			}
			t.Fatalf("case %d: %s", tc, fmt.Sprintf(format, args...))
		}

		steps := 10 + rng.IntN(31)
		for step := 0; step < steps; step++ {
			roll := rng.IntN(100)
			switch {
			case roll < 12:
				id := a.NewSegment()
				mid := m.newSegment()
				logBuf = append(logBuf, fmt.Sprintf("step %d: NewSegment() -> impl=%d naive=%d", step, id, mid))
				if id != mid {
					failf("NewSegment id mismatch")
				}
			case roll < 62:
				if len(m.alive) <= 1 {
					id := a.NewSegment()
					mid := m.newSegment()
					logBuf = append(logBuf, fmt.Sprintf("step %d: bootstrap NewSegment() -> %d/%d", step, id, mid))
				}
				s := 1 + rng.IntN(len(m.alive)-1)
				var hint int
				switch rng.IntN(10) {
				case 0:
					hint = -1
				case 1:
					hint = -2
				case 2:
					hint = e * x
				default:
					hint = rng.IntN(e * x)
				}
				got, gerr := a.AllocPage(s, hint)
				want, reason, _ := m.allocPage(s, hint)
				implOK := gerr == nil
				naiveOK := !isNaiveError(reason)
				match := got == want && implOK == naiveOK && (!implOK || true)
				if !implOK && errName(gerr) != reason {
					match = false
				}
				verdict := "一致"
				if !match {
					verdict = "不一致"
				}
				logBuf = append(logBuf, fmt.Sprintf(
					"step %d: AllocPage(s=%d,hint=%d) -> impl=(%d,%s) naive=(%d,%s) 判定:%s",
					step, s, hint, got, errName(gerr), want, reason, verdict))
				if !match {
					failf("AllocPage mismatch s=%d hint=%d", s, hint)
				}
			case roll < 92:
				if len(m.alive) <= 1 {
					continue
				}
				s := 1 + rng.IntN(len(m.alive)-1)
				p := rng.IntN(e*x+2) - 1
				gerr := a.FreePage(s, p)
				reason, _ := m.freePage(s, p)
				match := (gerr == nil && !isNaiveError(reason)) ||
					(gerr != nil && errName(gerr) == reason)
				verdict := "一致"
				if !match {
					verdict = "不一致"
				}
				logBuf = append(logBuf, fmt.Sprintf(
					"step %d: FreePage(s=%d,p=%d) -> impl=%s naive=%s 判定:%s",
					step, s, p, errName(gerr), reason, verdict))
				if !match {
					failf("FreePage mismatch s=%d p=%d", s, p)
				}
			default:
				if len(m.alive) <= 1 {
					continue
				}
				s := 1 + rng.IntN(len(m.alive)-1)
				gerr := a.FreeSegment(s)
				reason, _ := m.freeSegment(s)
				match := (gerr == nil && reason == "OK:segment") ||
					(gerr != nil && errName(gerr) == reason)
				verdict := "一致"
				if !match {
					verdict = "不一致"
				}
				logBuf = append(logBuf, fmt.Sprintf(
					"step %d: FreeSegment(s=%d) -> impl=%s naive=%s 判定:%s",
					step, s, errName(gerr), reason, verdict))
				if !match {
					failf("FreeSegment mismatch s=%d", s)
				}
			}

			// 逐步对照区段状态、used、非满队列。
			implStates := a.ExtentStates()
			for i := 0; i < e; i++ {
				wantState := m.state[i]
				var wantOwner int
				if wantState == "SEG" {
					wantOwner = m.owner[i]
				}
				if implStates[i].State != wantState ||
					implStates[i].Used != m.used[i] ||
					implStates[i].Owner != wantOwner {
					failf("step %d extent %d: impl=%+v naive=(%s,owner=%d,used=%d)",
						step, i, implStates[i], wantState, wantOwner, m.used[i])
				}
			}
			for s := 1; s < len(m.alive); s++ {
				if !m.alive[s] {
					if _, uerr := a.Used(s); uerr == nil {
						failf("step %d: dead segment %d still alive in impl", step, s)
					}
					continue
				}
				if got, _ := a.Used(s); got != m.sused[s] {
					failf("step %d segment %d used: impl=%d naive=%d", step, s, got, m.sused[s])
				}
				iq := implQueue(a, s)
				nq := m.queue[s]
				if len(iq) != len(nq) {
					failf("step %d segment %d queue length: impl=%v naive=%v", step, s, iq, nq)
				}
				for k := range iq {
					if iq[k] != nq[k] {
						failf("step %d segment %d queue order: impl=%v naive=%v", step, s, iq, nq)
					}
				}
			}
			checkInvariants(t, a, fmt.Sprintf("case %d step %d", tc, step))
		}

		// 每组打印完整输入/输出/判定日志（go test -v 可见）。
		for _, line := range logBuf {
			t.Log(line)
		}
	}
}
