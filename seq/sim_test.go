package seq

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 朴素模拟：直接按规格规则逐步写成，与被测实现不共享任何代码，
// 用于对拍 Allocator 的每一次签发、跳号计数与拒绝原因。
type sim struct {
	k    int
	m    int64
	next []int64
	act  []bool
	j    int64

	seen map[int64]int // 已签发编号 -> 签发系统，用于全局唯一性检查
	last []int64       // 各系统最近一次签发的编号
}

func newSim(k int, m int64) *sim {
	s := &sim{
		k:    k,
		m:    m,
		next: make([]int64, k),
		act:  make([]bool, k),
		last: make([]int64, k),
		seen: make(map[int64]int),
	}
	for i := range s.next {
		s.next[i] = 1
	}
	s.act[0] = true
	return s
}

func simAlign(x, c, m int64) int64 {
	r := x % m
	if r <= c {
		return x + c - r
	}
	return x + m - r + c
}

func (s *sim) activeCount() int {
	n := 0
	for _, a := range s.act {
		if a {
			n++
		}
	}
	return n
}

func (s *sim) step() int64 {
	if s.activeCount() >= 2 {
		return s.m
	}
	return 1
}

func (s *sim) high() int64 {
	h := int64(0)
	for _, n := range s.next {
		if n > h {
			h = n
		}
	}
	return h
}

func (s *sim) reserve(sys, n int) ([]int64, error) {
	if sys < 1 || sys > s.k {
		return nil, ErrParam
	}
	if n < 1 || n > 1000 {
		return nil, ErrParam
	}
	i := sys - 1
	if !s.act[i] {
		return nil, ErrInactive
	}
	st := s.step()
	if s.next[i]+int64(n-1)*st > MaxID {
		return nil, ErrExhausted
	}
	ids := make([]int64, n)
	for j := range ids {
		ids[j] = s.next[i] + int64(j)*st
	}
	s.next[i] += int64(n) * st
	return ids, nil
}

func (s *sim) observe(sys int, id int64) error {
	if sys < 1 || sys > s.k {
		return ErrParam
	}
	if id < 1 || id > MaxID {
		return ErrParam
	}
	i := sys - 1
	if !s.act[i] {
		return ErrInactive
	}
	if id < s.next[i] {
		return nil
	}
	var nn int64
	if s.step() == 1 {
		nn = id + 1
	} else {
		nn = simAlign(id+1, int64(i), s.m)
	}
	s.j += nn - s.next[i]
	s.next[i] = nn
	return nil
}

func (s *sim) join(role, sys int) error {
	if sys < 1 || sys > s.k {
		return ErrParam
	}
	if role < 1 || role > 2 {
		return ErrParam
	}
	if role != 2 {
		return ErrPermission
	}
	i := sys - 1
	if s.act[i] {
		return ErrState
	}
	h := s.high()
	if s.activeCount() == 1 {
		t := 0
		for x := range s.act {
			if s.act[x] {
				t = x
			}
		}
		al := simAlign(s.next[t], int64(t), s.m)
		s.j += al - s.next[t]
		s.next[t] = al
	}
	base := s.next[i]
	if h > base {
		base = h
	}
	al := simAlign(base, int64(i), s.m)
	s.j += al - s.next[i]
	s.next[i] = al
	s.act[i] = true
	return nil
}

func (s *sim) leave(role, sys int) error {
	if sys < 1 || sys > s.k {
		return ErrParam
	}
	if role < 1 || role > 2 {
		return ErrParam
	}
	if role != 2 {
		return ErrPermission
	}
	i := sys - 1
	if !s.act[i] {
		return ErrState
	}
	if s.activeCount() == 1 {
		return ErrLast
	}
	s.act[i] = false
	if s.activeCount() == 1 {
		r := 0
		for x := range s.act {
			if s.act[x] {
				r = x
			}
		}
		h := s.high()
		s.j += h - s.next[r]
		s.next[r] = h
	}
	return nil
}

// checkIssued 校验一次成功签发的不变量：全局唯一、不超 MaxID、
// 每系统严格递增、步长 m 期间落在本系统同余类上。
func (s *sim) checkIssued(t *testing.T, sys int, ids []int64, strideUsed int64) error {
	t.Helper()
	i := sys - 1
	for _, id := range ids {
		if id < 1 || id > MaxID {
			return fmt.Errorf("编号 %d 超出 [1, MaxID]", id)
		}
		if prev, dup := s.seen[id]; dup {
			return fmt.Errorf("编号 %d 被系统 %d 与系统 %d 重复签发", id, prev, sys)
		}
		if id <= s.last[i] {
			return fmt.Errorf("系统 %d 签发 %d 未严格递增（上次 %d）", sys, id, s.last[i])
		}
		if strideUsed > 1 && id%s.m != int64(i) {
			return fmt.Errorf("系统 %d 签发 %d 不满足模 %d 余 %d", sys, id, s.m, i)
		}
		s.seen[id] = sys
		s.last[i] = id
	}
	return nil
}

type op struct {
	kind    string
	s, role int
	n       int
	id      int64
}

func (o op) String() string {
	switch o.kind {
	case "Issue":
		return fmt.Sprintf("Issue(%d)", o.s)
	case "Reserve":
		return fmt.Sprintf("Reserve(%d, %d)", o.s, o.n)
	case "Observe":
		return fmt.Sprintf("Observe(%d, %d)", o.s, o.id)
	case "Join":
		return fmt.Sprintf("Join(role=%d, %d)", o.role, o.s)
	case "Leave":
		return fmt.Sprintf("Leave(role=%d, %d)", o.role, o.s)
	}
	return "?"
}

func genOps(rng *rand.Rand, k int, sm *sim, count int) []op {
	ops := make([]op, 0, count)
	randS := func() int {
		if rng.Intn(100) < 85 {
			return 1 + rng.Intn(k)
		}
		return []int{0, k + 1}[rng.Intn(2)] // 越界参数
	}
	for len(ops) < count {
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
			ops = append(ops, op{kind: "Issue", s: randS()})
		case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
			40, 41, 42, 43, 44:
			n := 1 + rng.Intn(20)
			if rng.Intn(100) < 5 {
				n = 1 + rng.Intn(1000)
			}
			if rng.Intn(100) < 8 {
				n = []int{0, 1001}[rng.Intn(2)] // 越界参数
			}
			ops = append(ops, op{kind: "Reserve", s: randS(), n: n})
		case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54,
			55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
			var id int64
			switch rng.Intn(100) {
			case 0, 1, 2:
				id = 0 // 越界参数
			case 3, 4:
				id = MaxID + 1 // 越界参数
			case 5, 6, 7, 8, 9:
				id = MaxID - int64(rng.Intn(5)) // 逼近上限
			default:
				target := rng.Intn(k)
				id = sm.next[target] + int64(rng.Intn(31)) - 2
				if id < 1 {
					id = 1
				}
				if id > MaxID {
					id = MaxID
				}
			}
			ops = append(ops, op{kind: "Observe", s: randS(), id: id})
		case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74,
			75, 76, 77, 78, 79:
			role := 2
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
				10, 11, 12, 13, 14, 15, 16, 17, 18, 19:
				role = 1 // 权限不足
			case 20, 21, 22, 23, 24:
				role = []int{0, 3}[rng.Intn(2)] // 越界参数
			}
			ops = append(ops, op{kind: "Join", s: randS(), role: role})
		default:
			role := 2
			if rng.Intn(100) < 15 {
				role = 1
			}
			ops = append(ops, op{kind: "Leave", s: randS(), role: role})
		}
	}
	return ops
}

// 对拍：2000 组随机操作序列，逐步比较 Allocator 与朴素模拟的
// 签发编号、拒绝原因与跳号计数 J，并做全局唯一性检查。
func TestRandomReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	const sequences = 2000
	for seqNo := 0; seqNo < sequences; seqNo++ {
		k := 2 + rng.Intn(7)
		m := int64(k) + int64(rng.Intn(17-k))
		a, err := New(k, m)
		if err != nil {
			t.Fatalf("seq#%d New(%d, %d): %v", seqNo, k, m, err)
		}
		sm := newSim(k, m)
		ops := genOps(rng, k, sm, 40)
		t.Logf("seq#%d K=%d m=%d 共 %d 个操作", seqNo, k, m, len(ops))
		for opNo, o := range ops {
			var gotIDs, wantIDs []int64
			var gotErr, wantErr error
			strideUsed := sm.step()
			switch o.kind {
			case "Issue":
				var id int64
				id, gotErr = a.Issue(o.s)
				wantIDs, wantErr = sm.reserve(o.s, 1)
				if gotErr == nil {
					gotIDs = []int64{id}
				}
			case "Reserve":
				gotIDs, gotErr = a.Reserve(o.s, o.n)
				wantIDs, wantErr = sm.reserve(o.s, o.n)
			case "Observe":
				gotErr = a.Observe(o.s, o.id)
				wantErr = sm.observe(o.s, o.id)
			case "Join":
				gotErr = a.Join(o.role, o.s)
				wantErr = sm.join(o.role, o.s)
			case "Leave":
				gotErr = a.Leave(o.role, o.s)
				wantErr = sm.leave(o.role, o.s)
			}

			basis := "判定: 与朴素模拟输出一致"
			if gotErr != wantErr || !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Errorf("seq#%d op#%d %s: 得到 ids=%v err=%v, 朴素模拟 ids=%v err=%v",
					seqNo, opNo, o, gotIDs, gotErr, wantIDs, wantErr)
				break
			}
			if gotErr == nil && (o.kind == "Issue" || o.kind == "Reserve") {
				if ierr := sm.checkIssued(t, o.s, gotIDs, strideUsed); ierr != nil {
					t.Errorf("seq#%d op#%d %s: 不变量违反: %v", seqNo, opNo, o, ierr)
					break
				}
				basis = "判定: 与朴素模拟一致；全局唯一/递增/同余类检查通过"
			}
			if a.J() != sm.j {
				t.Errorf("seq#%d op#%d %s: J=%d, 朴素模拟 J=%d", seqNo, opNo, o, a.J(), sm.j)
				break
			}
			stateOK := true
			for s2 := 1; s2 <= k; s2++ {
				if a.Next(s2) != sm.next[s2-1] || a.Active(s2) != sm.act[s2-1] {
					t.Errorf("seq#%d op#%d %s: 系统 %d 状态分歧 next=%d/%d active=%v/%v",
						seqNo, opNo, o, s2, a.Next(s2), sm.next[s2-1], a.Active(s2), sm.act[s2-1])
					stateOK = false
				}
			}
			if !stateOK {
				break
			}
			t.Logf("seq#%d op#%d 输入=%s 输出=ids:%v err:%v J=%d %s",
				seqNo, opNo, o, gotIDs, errString(gotErr), sm.j, basis)
		}
		if t.Failed() {
			return
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
