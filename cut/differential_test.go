package cut_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/cut"
	"ontology/seq"
	"ontology/stride"
)

// naive 是按规则逐条直写的朴素模拟，作为对拍参照。
type naive struct {
	k      int
	m      int64
	next   []int64
	active []bool
	j      int64
}

func newNaive(k int, m int64) *naive {
	n := &naive{k: k, m: m, next: make([]int64, k+1), active: make([]bool, k+1)}
	for i := 1; i <= k; i++ {
		n.next[i] = 1
	}
	n.active[1] = true
	return n
}

func naiveAlignUp(x, c, m int64) int64 {
	y := x
	for ((y%m)+m)%m != c {
		y++
	}
	return y
}

func (n *naive) activeCount() int {
	c := 0
	for i := 1; i <= n.k; i++ {
		if n.active[i] {
			c++
		}
	}
	return c
}

func (n *naive) stride() int64 {
	if n.activeCount() == 1 {
		return 1
	}
	return n.m
}

func (n *naive) highWater() int64 {
	h := n.next[1]
	for i := 2; i <= n.k; i++ {
		if n.next[i] > h {
			h = n.next[i]
		}
	}
	return h
}

func (n *naive) validSys(s int) bool { return s >= 1 && s <= n.k }

func (n *naive) issue(s int) ([]int64, error) { return n.reserve(s, 1) }

func (n *naive) reserve(s, num int) ([]int64, error) {
	if !n.validSys(s) || num < 1 || num > 1000 {
		return nil, seq.ErrParam
	}
	if !n.active[s] {
		return nil, seq.ErrInactive
	}
	st := n.stride()
	if n.next[s]+int64(num-1)*st > stride.MaxID {
		return nil, seq.ErrExhausted
	}
	ids := make([]int64, num)
	for i := range ids {
		ids[i] = n.next[s] + int64(i)*st
	}
	n.next[s] += int64(num) * st
	return ids, nil
}

func (n *naive) observe(s int, id int64) error {
	if !n.validSys(s) || id < 1 || id > stride.MaxID {
		return seq.ErrParam
	}
	if !n.active[s] {
		return seq.ErrInactive
	}
	if id < n.next[s] {
		return nil
	}
	var nv int64
	if n.stride() == 1 {
		nv = id + 1
	} else {
		nv = naiveAlignUp(id+1, int64(s-1), n.m)
	}
	n.j += nv - n.next[s]
	n.next[s] = nv
	return nil
}

func (n *naive) join(role, s int) error {
	if !n.validSys(s) || role < 1 || role > 2 {
		return seq.ErrParam
	}
	if role != 2 {
		return seq.ErrPermission
	}
	if n.active[s] {
		return seq.ErrState
	}
	h := n.highWater()
	if n.activeCount() == 1 {
		t := 0
		for i := 1; i <= n.k; i++ {
			if n.active[i] {
				t = i
			}
		}
		nt := naiveAlignUp(n.next[t], int64(t-1), n.m)
		n.j += nt - n.next[t]
		n.next[t] = nt
	}
	base := n.next[s]
	if h > base {
		base = h
	}
	ns := naiveAlignUp(base, int64(s-1), n.m)
	n.j += ns - n.next[s]
	n.next[s] = ns
	n.active[s] = true
	return nil
}

func (n *naive) leave(role, s int) error {
	if !n.validSys(s) || role < 1 || role > 2 {
		return seq.ErrParam
	}
	if role != 2 {
		return seq.ErrPermission
	}
	if !n.active[s] {
		return seq.ErrState
	}
	if n.activeCount() == 1 {
		return seq.ErrLast
	}
	n.active[s] = false
	if n.activeCount() == 1 {
		r := 0
		for i := 1; i <= n.k; i++ {
			if n.active[i] {
				r = i
			}
		}
		h := n.highWater()
		if h > n.next[r] {
			n.j += h - n.next[r]
			n.next[r] = h
		}
	}
	return nil
}

type op struct {
	kind   string
	sys, n int
	id     int64
	role   int
}

func genOps(r *rand.Rand, k int, count int) []op {
	ops := make([]op, 0, count)
	for i := 0; i < count; i++ {
		sys := 1 + r.Intn(k)
		if r.Intn(20) == 0 { // 5% 越界系统号
			sys = r.Intn(k + 2)
		}
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			ops = append(ops, op{kind: "Issue", sys: sys})
		case 4, 5:
			num := 1 + r.Intn(1000)
			if r.Intn(20) == 0 {
				num = r.Intn(1002) // 可能越界
			}
			ops = append(ops, op{kind: "Reserve", sys: sys, n: num})
		case 6, 7:
			id := int64(1 + r.Intn(200))
			if r.Intn(20) == 0 {
				id = int64(r.Intn(3)) // 可能为 0
			}
			ops = append(ops, op{kind: "Observe", sys: sys, id: id})
		case 8:
			role := 2
			if r.Intn(10) == 0 {
				role = r.Intn(4) // 可能非管理员或越界
			}
			ops = append(ops, op{kind: "Join", sys: sys, role: role})
		case 9:
			role := 2
			if r.Intn(10) == 0 {
				role = r.Intn(4)
			}
			ops = append(ops, op{kind: "Leave", sys: sys, role: role})
		}
	}
	return ops
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// 对拍：2000 组随机操作序列，逐步比较真实实现与朴素模拟的
// 输出（编号、错误、J、全部 next 与活跃标志），并做全局唯一性检查。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	totalIssued := 0
	for seqNo := 0; seqNo < sequences; seqNo++ {
		seen := make(map[int64]bool) // 单序列内全局唯一性检查
		r := rand.New(rand.NewSource(int64(seqNo)))
		k := 2 + r.Intn(7)
		m := int64(k + r.Intn(17-k))
		real, err := seq.New(k, m)
		if err != nil {
			t.Fatalf("seq=%d New(%d,%d): %v", seqNo, k, m, err)
		}
		sim := newNaive(k, m)
		ops := genOps(r, k, 10+r.Intn(30))
		lastIssued := make(map[int]int64, k) // 每系统签发严格递增检查

		for step, o := range ops {
			var realIDs, simIDs []int64
			var realErr, simErr error
			switch o.kind {
			case "Issue":
				var id int64
				id, realErr = real.Issue(o.sys)
				if realErr == nil {
					realIDs = []int64{id}
				}
				simIDs, simErr = sim.issue(o.sys)
			case "Reserve":
				realIDs, realErr = real.Reserve(o.sys, o.n)
				simIDs, simErr = sim.reserve(o.sys, o.n)
			case "Observe":
				realErr = real.Observe(o.sys, o.id)
				simErr = sim.observe(o.sys, o.id)
			case "Join":
				realErr = cut.Join(real, o.role, o.sys)
				simErr = sim.join(o.role, o.sys)
			case "Leave":
				realErr = cut.Leave(real, o.role, o.sys)
				simErr = sim.leave(o.role, o.sys)
			}

			ok := sameErr(realErr, simErr)
			if ok && realErr == nil && len(realIDs) != len(simIDs) {
				ok = false
			}
			if ok {
				for i := range realIDs {
					if realIDs[i] != simIDs[i] {
						ok = false
					}
				}
			}
			t.Logf("seq=%d step=%d 输入=%+v 输出: real(ids=%v,err=%v) naive(ids=%v,err=%v) 判定: 输出一致=%v",
				seqNo, step, o, realIDs, realErr, simIDs, simErr, ok)
			if !ok {
				t.Fatalf("对拍失败: seq=%d step=%d op=%+v real(ids=%v,err=%v) naive(ids=%v,err=%v)",
					seqNo, step, o, realIDs, realErr, simIDs, simErr)
			}

			// 状态一致性：J、全部 next、活跃标志。
			stateOK := real.J() == sim.j
			for s := 1; s <= k && stateOK; s++ {
				stateOK = real.NextOf(s) == sim.next[s] && real.Active(s) == sim.active[s]
			}
			t.Logf("seq=%d step=%d 状态: J=%d 判定: 状态一致=%v", seqNo, step, real.J(), stateOK)
			if !stateOK {
				t.Fatalf("状态分叉: seq=%d step=%d op=%+v realJ=%d naiveJ=%d",
					seqNo, step, o, real.J(), sim.j)
			}

			// 签发编号的性质检查。
			for _, id := range realIDs {
				if id < 1 || id > stride.MaxID {
					t.Fatalf("seq=%d id=%d 超出 [1,MaxID]", seqNo, id)
				}
				if seen[id] {
					t.Fatalf("seq=%d id=%d 被重复签发", seqNo, id)
				}
				seen[id] = true
				totalIssued++
				if id <= lastIssued[o.sys] {
					t.Fatalf("seq=%d 系统%d 签发未严格递增: %d<=%d",
						seqNo, o.sys, id, lastIssued[o.sys])
				}
				lastIssued[o.sys] = id
				if real.ActiveCount() >= 2 && id%m != int64(o.sys-1) {
					t.Fatalf("seq=%d id=%d 不满足模%d余%d", seqNo, id, m, o.sys-1)
				}
			}
		}
	}
	t.Logf("对拍完成: %d 组序列，共签发 %d 个编号，序列内均全局唯一", sequences, totalIssued)
}

// 重放确定性：相同操作序列重放得到相同编号与 J。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	k, m := 4, int64(6)
	ops := genOps(r, k, 200)
	run := func() ([]string, int64) {
		st, err := seq.New(k, m)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, o := range ops {
			switch o.kind {
			case "Issue":
				id, err := st.Issue(o.sys)
				out = append(out, fmt.Sprintf("%d/%v", id, err))
			case "Reserve":
				ids, err := st.Reserve(o.sys, o.n)
				out = append(out, fmt.Sprintf("%v/%v", ids, err))
			case "Observe":
				out = append(out, fmt.Sprintf("%v", st.Observe(o.sys, o.id)))
			case "Join":
				out = append(out, fmt.Sprintf("%v", cut.Join(st, o.role, o.sys)))
			case "Leave":
				out = append(out, fmt.Sprintf("%v", cut.Leave(st, o.role, o.sys)))
			}
		}
		return out, st.J()
	}
	out1, j1 := run()
	out2, j2 := run()
	if j1 != j2 {
		t.Fatalf("重放 J 不一致: %d vs %d", j1, j2)
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("重放第 %d 步输出不一致: %s vs %s", i, out1[i], out2[i])
		}
	}
}
