package core

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func genOps(rng *rand.Rand, n, opCount int, users int64) []fuzzOp {
	var ops []fuzzOp
	now := int64(0)
	for i := 0; i < opCount; i++ {
		// 时间多数非减，少量回退以触发时钟错误，含到期边界 now==until。
		switch rng.Intn(10) {
		case 0:
			now = rng.Int63n(now + 2) // 可能回退
		default:
			now += int64(rng.Intn(4))
		}
		a := int64(1 + rng.Intn(int(users)+2)) // 偶尔生成陌生人
		b := int64(1 + rng.Intn(int(users)+2))
		switch rng.Intn(8) {
		case 0:
			ops = append(ops, fuzzOp{name: "Join", now: now, a: a})
		case 1:
			ops = append(ops, fuzzOp{name: "Leave", now: now, a: a})
		case 2:
			role := Admin
			if rng.Intn(2) == 0 {
				role = Member
			}
			if rng.Intn(10) == 0 {
				role = Owner // 故意非法
			}
			ops = append(ops, fuzzOp{name: "SetRole", now: now, a: a, b: b, role: role, hasRole: true})
		case 3:
			ops = append(ops, fuzzOp{name: "Transfer", now: now, a: a, b: b})
		case 4, 5:
			// until：半数 > now，半数 <= now 触发参数非法；个别恰好等于某已存在 until 时刻。
			until := now + 1 + int64(rng.Intn(30))
			if rng.Intn(4) == 0 {
				until = now - int64(rng.Intn(5))
			}
			ops = append(ops, fuzzOp{name: "Mute", now: now, a: a, b: b, until: until, hasUntil: true})
		case 6:
			ops = append(ops, fuzzOp{name: "Unmute", now: now, a: a, b: b})
		case 7:
			ops = append(ops, fuzzOp{name: "TakeMic", now: now, a: a})
		default:
			ops = append(ops, fuzzOp{name: "DropMic", now: now, a: a})
		}
	}
	return ops
}

func (r *Room) applyReal(op fuzzOp) error {
	switch op.name {
	case "Join":
		return r.Join(op.now, op.a)
	case "Leave":
		return r.Leave(op.now, op.a)
	case "SetRole":
		return r.SetRole(op.now, op.a, op.b, op.role)
	case "Transfer":
		return r.Transfer(op.now, op.a, op.b)
	case "Mute":
		return r.Mute(op.now, op.a, op.b, op.until)
	case "Unmute":
		return r.Unmute(op.now, op.a, op.b)
	case "TakeMic":
		return r.TakeMic(op.now, op.a)
	case "DropMic":
		return r.DropMic(op.now, op.a)
	}
	return ErrBadArgument
}

func checkInvariants(t *testing.T, r *Room, now int64, log *strings.Builder, seq int) {
	t.Helper()
	// 麦上无生效禁言者。
	for i, u := range r.slots {
		if u != 0 && r.muted(u, now) {
			fmt.Fprintf(log, "不变量违反: 麦位 %d 用户 %d 在 now=%d 仍被禁言\n", i, u, now)
			t.Fatalf("麦上存在生效禁言者 (seq=%d)", seq)
		}
	}
	// 不存在“有空麦且队列中有未禁言者”。
	hasFree := r.firstFreeSlot() >= 0
	if hasFree {
		for n := r.head.next; n != r.tail; n = n.next {
			if !r.muted(n.user, now) {
				t.Fatalf("有空麦但队列存在未禁言者 %d (seq=%d)", n.user, seq)
			}
		}
	}
	// 每人至多出现在一个麦位或队列中一次。
	seen := map[int64]int{}
	for _, u := range r.slots {
		if u != 0 {
			seen[u]++
		}
	}
	for n := r.head.next; n != r.tail; n = n.next {
		seen[n.user]++
	}
	for u, c := range seen {
		if c > 1 {
			t.Fatalf("用户 %d 在麦序中出现 %d 次 (seq=%d)", u, c, seq)
		}
	}
	// 房间非空时恰有一个 Owner。
	owners := 0
	for u, lv := range r.roles {
		if lv == Owner {
			owners++
		}
		if !r.present(u) {
			t.Fatalf("角色表脏数据 %d", u)
		}
	}
	if len(r.roles) > 0 && owners != 1 {
		t.Fatalf("非空房间 Owner 数 = %d (seq=%d)", owners, seq)
	}
	if len(r.roles) == 0 && owners != 0 {
		t.Fatalf("空房间仍有 Owner")
	}
}

func statesEqual(r *Room, n *naiveRoom) (bool, string) {
	if len(r.slots) != len(n.slots) {
		return false, "麦位数"
	}
	for i := range r.slots {
		if r.slots[i] != n.slots[i] {
			return false, fmt.Sprintf("麦位 %d: real=%d naive=%d", i, r.slots[i], n.slots[i])
		}
	}
	rq := queueOf(r)
	if len(rq) != len(n.queue) {
		return false, fmt.Sprintf("队列长度 real=%v naive=%v", rq, n.queue)
	}
	for i := range rq {
		if rq[i] != n.queue[i] {
			return false, fmt.Sprintf("队列位置 %d: real=%d naive=%d", i, rq[i], n.queue[i])
		}
	}
	if len(r.roles) != len(n.roles) {
		return false, "角色集合大小"
	}
	for u, lv := range n.roles {
		if r.roles[u] != lv {
			return false, fmt.Sprintf("角色 %d: real=%d naive=%d", u, r.roles[u], lv)
		}
	}
	if len(r.mutes) != len(n.mutes) {
		return false, "禁言集合大小"
	}
	for u, m := range n.mutes {
		if rm, ok := r.mutes[u]; !ok || rm != m {
			return false, fmt.Sprintf("禁言 %d: real=%+v naive=%+v", u, rm, m)
		}
	}
	if r.maxNow != n.maxNow {
		return false, fmt.Sprintf("maxNow real=%d naive=%d", r.maxNow, n.maxNow)
	}
	return true, ""
}

// TestFuzzVsNaive 1500 组随机操作序列逐步对拍朴素模拟器，
// 日志打印每个操作的输入、输出与判定依据。
func TestFuzzVsNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过随机对拍")
	}
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g*7919 + 1)))
		m := 1 + rng.Intn(8)
		users := int64(2 + rng.Intn(7))
		ops := genOps(rng, m, 80, users)

		real := New(m)
		na := newNaive(m)
		var log strings.Builder
		fmt.Fprintf(&log, "=== 组 %d: M=%d 用户池=%d 操作=%d ===\n", g, m, users, len(ops))

		for seq, op := range ops {
			got := real.applyReal(op)
			want := na.apply(op)
			fmt.Fprintf(&log, "[%03d] 输入: %s\n", seq, op.String())
			if !errorsIs(got, want) {
				fmt.Fprintf(&log, "判定分歧: real=%v naive=%v\n", got, want)
				fmt.Fprintf(&log, "real 状态: slots=%v queue=%v roles=%v mutes=%v maxNow=%d\n",
					slotsOf(real), queueOf(real), real.roles, real.mutes, real.maxNow)
				fmt.Fprintf(&log, "naive 状态: slots=%v queue=%v roles=%v mutes=%v maxNow=%d\n",
					na.slots, na.queue, na.roles, na.mutes, na.maxNow)
				t.Fatalf("组 %d seq %d 输出分歧:\n%s", g, seq, log.String())
			}
			basis := "成功接受并出口补麦"
			if got != nil {
				basis = "拒绝(" + got.Error() + ")，入口补麦随快照回滚，时钟不推进"
			}
			fmt.Fprintf(&log, "      输出: %v | 判定依据: %s\n", got, basis)

			if ok, diff := statesEqual(real, na); !ok {
				fmt.Fprintf(&log, "状态分歧: %s\n", diff)
				t.Fatalf("组 %d seq %d 状态分歧:\n%s", g, seq, log.String())
			}
			// 不变量以房间当前时钟 maxNow 判定：被拒操作不推进时钟，
			// 其携带的（更早）now 不能用来解读当前房间状态。
			checkInvariants(t, real, real.maxNow, &log, seq)
			// 确定性由固定种子 + 链表插入序（无 map 迭代依赖）保证；
			// 另有 TestReplayDeterminism 对同序列重放做交叉验证。
		}
		// 每组首组完整日志输出，其余仅失败时随 Fatal 打印。
		if g == 0 {
			t.Logf("组 0 完整日志（输入/输出/判定依据）:\n%s", log.String())
		}
	}
}

func errorsIs(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got == want
}

// TestConcurrentLinearizable 并发冒烟：多 goroutine 对同一房间操作，
// 配合 -race 检测数据竞争；结束后不变量必须成立。
func TestConcurrentLinearizable(t *testing.T) {
	r := New(4)
	mustOK(t, "owner", r.Join(0, 1))
	var wg sync.WaitGroup
	for g := int64(2); g <= 10; g++ {
		wg.Add(1)
		go func(u int64) {
			defer wg.Done()
			r.Join(u*10, u)
			r.TakeMic(u*10+1, u)
			if u == 2 {
				r.Mute(u*10+2, 1, 5, u*10+20)
			}
			r.TakeMic(u*10+3, u)
			r.DropMic(u*10+4, u)
		}(g)
	}
	wg.Wait()
	var log strings.Builder
	checkInvariants(t, r, 100, &log, 0)
}

// TestReplayDeterminism：相同操作序列在两个独立房间重放，终态完全一致。
func TestReplayDeterminism(t *testing.T) {
	for g := 0; g < 100; g++ {
		rng := rand.New(rand.NewSource(int64(g*104729 + 7)))
		m := 1 + rng.Intn(8)
		ops := genOps(rng, m, 120, int64(2+rng.Intn(7)))
		r1, r2 := New(m), New(m)
		var errs1, errs2 []string
		for _, op := range ops {
			e1 := r1.applyReal(op)
			e2 := r2.applyReal(op)
			if !errorsIs(e1, e2) {
				t.Fatalf("组 %d 重放输出分歧: %v vs %v (%s)", g, e1, e2, op)
			}
			if e1 != nil {
				errs1 = append(errs1, e1.Error())
				errs2 = append(errs2, e2.Error())
			}
		}
		n2 := &naiveRoom{
			m:      len(r2.slots),
			slots:  slotsOf(r2),
			roles:  r2.roles,
			mutes:  r2.mutes,
			queue:  queueOf(r2),
			maxNow: r2.maxNow,
		}
		if ok, diff := statesEqual(r1, n2); !ok {
			t.Fatalf("组 %d 重放终态分歧: %s", g, diff)
		}
		if len(errs1) != len(errs2) {
			t.Fatalf("组 %d 拒绝序列长度分歧", g)
		}
	}
}

// naiveRoom 是完全独立的逐步朴素模拟器：切片麦位 + 切片队列 + map 角色/禁言。
// 补麦、拒绝次序、回滚均按规格文字直接书写，不与实现共享任何代码。
type naiveRoom struct {
	m      int
	slots  []int64
	roles  map[int64]int
	mutes  map[int64]muteRec
	queue  []int64
	maxNow int64
}

func newNaive(m int) *naiveRoom {
	return &naiveRoom{
		m:     m,
		slots: make([]int64, m),
		roles: map[int64]int{},
		mutes: map[int64]muteRec{},
	}
}

func (n *naiveRoom) muted(u int64, now int64) bool {
	m, ok := n.mutes[u]
	return ok && now < m.until
}

func (n *naiveRoom) present(u int64) bool { _, ok := n.roles[u]; return ok }

// naiveFill 朴素补麦：有空麦就反复从队首找第一个未禁言者。
func (n *naiveRoom) fill(now int64) {
	for {
		slot := -1
		for i, u := range n.slots {
			if u == 0 {
				slot = i
				break
			}
		}
		if slot < 0 {
			return
		}
		idx := -1
		for i, u := range n.queue {
			if !n.muted(u, now) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		u := n.queue[idx]
		n.queue = append(n.queue[:idx], n.queue[idx+1:]...)
		n.slots[slot] = u
	}
}

func (n *naiveRoom) onMic(u int64) bool {
	for _, s := range n.slots {
		if s == u {
			return true
		}
	}
	return false
}

func (n *naiveRoom) inQueue(u int64) bool {
	for _, q := range n.queue {
		if q == u {
			return true
		}
	}
	return false
}

func (n *naiveRoom) removeFromMicOrQueue(u int64) {
	if idx := indexOf(n.queue, u); idx >= 0 {
		n.queue = append(n.queue[:idx], n.queue[idx+1:]...)
		return
	}
	for i, s := range n.slots {
		if s == u {
			n.slots[i] = 0
			return
		}
	}
}

func indexOf(q []int64, u int64) int {
	for i, x := range q {
		if x == u {
			return i
		}
	}
	return -1
}

type naiveState struct {
	slots  []int64
	roles  map[int64]int
	mutes  map[int64]muteRec
	queue  []int64
	maxNow int64
}

func (n *naiveRoom) save() naiveState {
	s := naiveState{slots: append([]int64(nil), n.slots...), maxNow: n.maxNow}
	s.roles = map[int64]int{}
	for u, lv := range n.roles {
		s.roles[u] = lv
	}
	s.mutes = map[int64]muteRec{}
	for u, m := range n.mutes {
		s.mutes[u] = m
	}
	s.queue = append([]int64(nil), n.queue...)
	return s
}

func (n *naiveRoom) restore(s naiveState) {
	n.slots = append([]int64(nil), s.slots...)
	n.roles = map[int64]int{}
	for u, lv := range s.roles {
		n.roles[u] = lv
	}
	n.mutes = map[int64]muteRec{}
	for u, m := range s.mutes {
		n.mutes[u] = m
	}
	n.queue = append([]int64(nil), s.queue...)
	n.maxNow = s.maxNow
}

func validTime(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

type fuzzOp struct {
	name              string
	now               int64
	a, b, until       int64
	role              int
	hasUntil, hasRole bool
}

func (op fuzzOp) String() string {
	parts := []string{op.name, "now=" + strconv.FormatInt(op.now, 10)}
	if op.a != 0 {
		parts = append(parts, "u="+strconv.FormatInt(op.a, 10))
	}
	if op.b != 0 {
		parts = append(parts, "target="+strconv.FormatInt(op.b, 10))
	}
	if op.hasUntil {
		parts = append(parts, "until="+strconv.FormatInt(op.until, 10))
	}
	if op.hasRole {
		parts = append(parts, "role="+strconv.Itoa(op.role))
	}
	return strings.Join(parts, " ")
}

// applyNaive 严格按“参数 > 时钟 > by > target > 等级 > 压制 > 状态类”次序，
// 入口补麦后判定、拒绝整体回滚、通过后出口补麦并推进时钟。
func (n *naiveRoom) apply(op fuzzOp) error {
	switch op.name {
	case "Join":
		if !validTime(op.now) || op.a <= 0 {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if n.present(op.a) {
			n.restore(s)
			return ErrDuplicate
		}
		if len(n.roles) == 0 {
			n.roles[op.a] = Owner
		} else {
			n.roles[op.a] = Member
		}
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "Leave":
		if !validTime(op.now) || op.a <= 0 {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if n.roles[op.a] == Owner && len(n.roles) > 1 {
			n.restore(s)
			return ErrMustTransfer
		}
		n.removeFromMicOrQueue(op.a)
		delete(n.roles, op.a)
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "SetRole":
		if !validTime(op.now) || op.a <= 0 || op.b <= 0 || (op.role != Admin && op.role != Member) {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if !n.present(op.b) {
			n.restore(s)
			return ErrNoTarget
		}
		if !(n.roles[op.a] > n.roles[op.b] && n.roles[op.a] > op.role) {
			n.restore(s)
			return ErrLowLevel
		}
		n.roles[op.b] = op.role
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "Transfer":
		if !validTime(op.now) || op.a <= 0 || op.b <= 0 || op.a == op.b {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if !n.present(op.b) {
			n.restore(s)
			return ErrNoTarget
		}
		if n.roles[op.a] != Owner {
			n.restore(s)
			return ErrLowLevel
		}
		n.roles[op.a] = Admin
		n.roles[op.b] = Owner
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "Mute":
		if !validTime(op.now) || op.a <= 0 || op.b <= 0 || op.until <= op.now {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if !n.present(op.b) {
			n.restore(s)
			return ErrNoTarget
		}
		if n.roles[op.a] <= n.roles[op.b] {
			n.restore(s)
			return ErrLowLevel
		}
		if old, ok := n.mutes[op.b]; ok && op.now < old.until && old.level > n.roles[op.a] {
			n.restore(s)
			return ErrSuppressed
		}
		n.mutes[op.b] = muteRec{until: op.until, level: n.roles[op.a]}
		if n.onMic(op.b) {
			for i, u := range n.slots {
				if u == op.b {
					n.slots[i] = 0
				}
			}
		}
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "Unmute":
		if !validTime(op.now) || op.a <= 0 || op.b <= 0 {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if !n.present(op.b) {
			n.restore(s)
			return ErrNoTarget
		}
		if n.roles[op.a] <= n.roles[op.b] {
			n.restore(s)
			return ErrLowLevel
		}
		if old, ok := n.mutes[op.b]; ok && op.now < old.until && old.level > n.roles[op.a] {
			n.restore(s)
			return ErrSuppressed
		}
		if !n.muted(op.b, op.now) {
			n.restore(s)
			return ErrNotMuted
		}
		delete(n.mutes, op.b)
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "TakeMic":
		if !validTime(op.now) || op.a <= 0 {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if n.muted(op.a, op.now) {
			n.restore(s)
			return ErrMuted
		}
		if n.onMic(op.a) || n.inQueue(op.a) {
			n.restore(s)
			return ErrDuplicate
		}
		slot := -1
		for i, u := range n.slots {
			if u == 0 {
				slot = i
				break
			}
		}
		if slot >= 0 {
			n.slots[slot] = op.a
		} else {
			n.queue = append(n.queue, op.a)
		}
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	case "DropMic":
		if !validTime(op.now) || op.a <= 0 {
			return ErrBadArgument
		}
		if op.now < n.maxNow {
			return ErrClockRewind
		}
		s := n.save()
		n.fill(op.now)
		if !n.present(op.a) {
			n.restore(s)
			return ErrNotInRoom
		}
		if !n.onMic(op.a) && !n.inQueue(op.a) {
			n.restore(s)
			return ErrNotInMic
		}
		n.removeFromMicOrQueue(op.a)
		n.fill(op.now)
		n.maxNow = op.now
		return nil
	}
	return ErrBadArgument
}
