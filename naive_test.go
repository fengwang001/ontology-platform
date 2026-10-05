package ontology_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology"
	"ontology/role"
)

// naive 是按规则逐步写成的朴素模拟：切片队列、整表拷贝回滚，
// 与 Manager 的链表/计数器实现相互独立，用于随机序列对照。
type naive struct {
	m      int
	maxNow int64
	roles  map[string]role.Level
	mics   []string
	queue  []string
	mutes  map[string]ontology.MuteView
}

func newNaive(m int) *naive {
	return &naive{
		m:     m,
		roles: make(map[string]role.Level),
		mics:  make([]string, m),
		mutes: make(map[string]ontology.MuteView),
	}
}

func (n *naive) muted(u string, now int64) bool {
	r, ok := n.mutes[u]
	return ok && now < r.Until
}

// fill 朴素补麦：每补一人从队首重新扫描，结果与单指针扫描等价。
func (n *naive) fill(now int64) {
	for {
		slot := -1
		for i, u := range n.mics {
			if u == "" {
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
		n.mics[slot] = n.queue[idx]
		n.queue = append(n.queue[:idx], n.queue[idx+1:]...)
	}
}

type naiveSnap struct {
	roles map[string]role.Level
	mics  []string
	queue []string
	mutes map[string]ontology.MuteView
}

func (n *naive) snapshot() naiveSnap {
	s := naiveSnap{
		roles: make(map[string]role.Level, len(n.roles)),
		mics:  append([]string(nil), n.mics...),
		queue: append([]string(nil), n.queue...),
		mutes: make(map[string]ontology.MuteView, len(n.mutes)),
	}
	for k, v := range n.roles {
		s.roles[k] = v
	}
	for k, v := range n.mutes {
		s.mutes[k] = v
	}
	return s
}

func (n *naive) restore(s naiveSnap) {
	n.roles, n.mics, n.queue, n.mutes = s.roles, s.mics, s.queue, s.mutes
}

// run 与 Manager.run 同构：时钟检查 → 快照 → 入口补麦 → 判定 → 出口补麦 → 推进时钟。
// 返回错误与判定依据（用于日志）。
func (n *naive) run(now int64, fn func() (error, string)) (error, string) {
	if now < 0 || now > 1_000_000_000_000 {
		return ontology.ErrParam, "参数非法: now 越界"
	}
	if now < n.maxNow {
		return ontology.ErrClock, "时钟回退"
	}
	snap := n.snapshot()
	n.fill(now)
	if err, reason := fn(); err != nil {
		n.restore(snap)
		return err, reason
	}
	n.fill(now)
	if now > n.maxNow {
		n.maxNow = now
	}
	return nil, "接受"
}

func (n *naive) level(u string) role.Level { return n.roles[u] }

func (n *naive) join(now int64, u string) (error, string) {
	if u == "" {
		return ontology.ErrParam, "参数非法: 空用户"
	}
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[u]; ok {
			return ontology.ErrDuplicate, "重复: 已在房间"
		}
		if len(n.roles) == 0 {
			n.roles[u] = role.Owner
		} else {
			n.roles[u] = role.Member
		}
		return nil, ""
	})
}

func (n *naive) setRole(now int64, by, target string, r role.Level) (error, string) {
	if r != role.Admin && r != role.Member {
		return ontology.ErrParam, "参数非法: r 只能是 Admin/Member"
	}
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[by]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if _, ok := n.roles[target]; !ok {
			return ontology.ErrTargetNotInRoom, "目标不在房间"
		}
		if lv := n.level(by); lv <= n.level(target) || lv <= r {
			return ontology.ErrLevel, "等级不足"
		}
		n.roles[target] = r
		return nil, ""
	})
}

func (n *naive) transfer(now int64, by, target string) (error, string) {
	if by == target {
		return ontology.ErrParam, "参数非法: by==target"
	}
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[by]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if _, ok := n.roles[target]; !ok {
			return ontology.ErrTargetNotInRoom, "目标不在房间"
		}
		if n.level(by) != role.Owner {
			return ontology.ErrLevel, "等级不足: 只有 Owner 能移交"
		}
		n.roles[target] = role.Owner
		n.roles[by] = role.Admin
		return nil, ""
	})
}

func (n *naive) leave(now int64, u string) (error, string) {
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[u]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if n.level(u) == role.Owner && len(n.roles) > 1 {
			return ontology.ErrMustTransfer, "须先移交"
		}
		delete(n.roles, u)
		for i, v := range n.mics {
			if v == u {
				n.mics[i] = ""
			}
		}
		for i, v := range n.queue {
			if v == u {
				n.queue = append(n.queue[:i], n.queue[i+1:]...)
				break
			}
		}
		return nil, ""
	})
}

func (n *naive) mute(now int64, by, target string, until int64) (error, string) {
	if until <= now {
		return ontology.ErrParam, "参数非法: until<=now"
	}
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[by]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if _, ok := n.roles[target]; !ok {
			return ontology.ErrTargetNotInRoom, "目标不在房间"
		}
		lv := n.level(by)
		if lv <= n.level(target) {
			return ontology.ErrLevel, "等级不足"
		}
		if rec, ok := n.mutes[target]; ok && now < rec.Until && rec.L0 > lv {
			return ontology.ErrSuppressed, "被压制: L0 高于当前等级"
		}
		n.mutes[target] = ontology.MuteView{Until: until, L0: lv}
		for i, v := range n.mics {
			if v == target {
				n.mics[i] = ""
			}
		}
		return nil, ""
	})
}

func (n *naive) unmute(now int64, by, target string) (error, string) {
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[by]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if _, ok := n.roles[target]; !ok {
			return ontology.ErrTargetNotInRoom, "目标不在房间"
		}
		lv := n.level(by)
		if lv <= n.level(target) {
			return ontology.ErrLevel, "等级不足"
		}
		rec, ok := n.mutes[target]
		active := ok && now < rec.Until
		if active && rec.L0 > lv {
			return ontology.ErrSuppressed, "被压制: L0 高于当前等级"
		}
		if !active {
			return ontology.ErrNotMuted, "未禁言"
		}
		delete(n.mutes, target)
		return nil, ""
	})
}

func (n *naive) takeMic(now int64, u string) (error, string) {
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[u]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		if n.muted(u, now) {
			return ontology.ErrMuted, "禁言中"
		}
		for _, v := range n.mics {
			if v == u {
				return ontology.ErrDuplicate, "重复: 已在麦上"
			}
		}
		for _, v := range n.queue {
			if v == u {
				return ontology.ErrDuplicate, "重复: 已在队列"
			}
		}
		slot := -1
		for i, v := range n.mics {
			if v == "" {
				slot = i
				break
			}
		}
		if slot >= 0 {
			n.mics[slot] = u
		} else {
			n.queue = append(n.queue, u)
		}
		return nil, ""
	})
}

func (n *naive) dropMic(now int64, u string) (error, string) {
	return n.run(now, func() (error, string) {
		if _, ok := n.roles[u]; !ok {
			return ontology.ErrNotInRoom, "操作者不在房间"
		}
		for i, v := range n.mics {
			if v == u {
				n.mics[i] = ""
				return nil, ""
			}
		}
		for i, v := range n.queue {
			if v == u {
				n.queue = append(n.queue[:i], n.queue[i+1:]...)
				return nil, ""
			}
		}
		return ontology.ErrNotInMicOrder, "不在麦序"
	})
}

// opData 为一条可重放的操作。
type opData struct {
	kind  int
	now   int64
	a, b  string
	lvl   role.Level
	until int64
}

const (
	opJoin = iota
	opSetRole
	opTransfer
	opLeave
	opMute
	opUnmute
	opTakeMic
	opDropMic
)

var opNames = []string{"Join", "SetRole", "Transfer", "Leave", "Mute", "Unmute", "TakeMic", "DropMic"}

func (o opData) String() string {
	switch o.kind {
	case opJoin, opLeave, opTakeMic, opDropMic:
		return fmt.Sprintf("%s(now=%d, %s)", opNames[o.kind], o.now, o.a)
	case opSetRole:
		return fmt.Sprintf("SetRole(now=%d, %s, %s, %d)", o.now, o.a, o.b, o.lvl)
	case opMute:
		return fmt.Sprintf("Mute(now=%d, %s, %s, until=%d)", o.now, o.a, o.b, o.until)
	default:
		return fmt.Sprintf("%s(now=%d, %s, %s)", opNames[o.kind], o.now, o.a, o.b)
	}
}

func (o opData) callManager(m *ontology.Manager) error {
	switch o.kind {
	case opJoin:
		return m.Join(o.now, o.a)
	case opSetRole:
		return m.SetRole(o.now, o.a, o.b, o.lvl)
	case opTransfer:
		return m.Transfer(o.now, o.a, o.b)
	case opLeave:
		return m.Leave(o.now, o.a)
	case opMute:
		return m.Mute(o.now, o.a, o.b, o.until)
	case opUnmute:
		return m.Unmute(o.now, o.a, o.b)
	case opTakeMic:
		return m.TakeMic(o.now, o.a)
	case opDropMic:
		return m.DropMic(o.now, o.a)
	}
	panic("bad op")
}

func (o opData) callNaive(n *naive) (error, string) {
	switch o.kind {
	case opJoin:
		return n.join(o.now, o.a)
	case opSetRole:
		return n.setRole(o.now, o.a, o.b, o.lvl)
	case opTransfer:
		return n.transfer(o.now, o.a, o.b)
	case opLeave:
		return n.leave(o.now, o.a)
	case opMute:
		return n.mute(o.now, o.a, o.b, o.until)
	case opUnmute:
		return n.unmute(o.now, o.a, o.b)
	case opTakeMic:
		return n.takeMic(o.now, o.a)
	case opDropMic:
		return n.dropMic(o.now, o.a)
	}
	panic("bad op")
}

var randUsers = []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6"}

// genSeq 生成一条随机操作序列（含时钟回退、非法参数等边界）。
func genSeq(rng *rand.Rand, steps int) []opData {
	ops := make([]opData, 0, steps)
	var now int64
	pickUser := func() string { return randUsers[rng.Intn(len(randUsers))] }
	// 前置若干 Join 与任免，保证房间有 Owner/Admin/Member，后续操作能进入深层判定。
	for _, u := range randUsers[:3+rng.Intn(4)] {
		now += rng.Int63n(5)
		ops = append(ops, opData{kind: opJoin, now: now, a: u})
	}
	now += rng.Int63n(5)
	ops = append(ops, opData{kind: opSetRole, now: now, a: randUsers[0], b: randUsers[1], lvl: role.Admin})
	now += rng.Int63n(5)
	ops = append(ops, opData{kind: opSetRole, now: now, a: randUsers[0], b: randUsers[2], lvl: role.Admin})
	for i := 0; i < steps; i++ {
		if rng.Intn(10) == 0 {
			now -= rng.Int63n(120) // 可能触发时钟回退或 now 越界
		} else {
			now += rng.Int63n(60)
		}
		// by 偏向先加入的用户（大概率为 Owner/Admin），以深入等级与压制判定。
		pickBy := func() string {
			if rng.Intn(10) < 6 {
				return randUsers[rng.Intn(3)]
			}
			return pickUser()
		}
		kind := rng.Intn(24)
		var op opData
		op.now = now
		switch {
		case kind < 2:
			op.kind = opJoin
			op.a = pickUser()
		case kind < 5:
			op.kind = opSetRole
			op.a, op.b = pickBy(), pickUser()
			op.lvl = []role.Level{role.Member, role.Admin, role.Admin, role.Owner, role.Level(0)}[rng.Intn(5)]
		case kind < 7:
			op.kind = opTransfer
			op.a, op.b = pickBy(), pickUser()
		case kind < 9:
			op.kind = opLeave
			op.a = pickUser()
		case kind < 13:
			op.kind = opMute
			op.a = pickBy()
			if rng.Intn(10) < 7 { // 目标偏向固定子集，提高压制碰撞率
				op.b = randUsers[3+rng.Intn(3)]
			} else {
				op.b = pickUser()
			}
			op.until = now + rng.Int63n(241) - 60 // 可能 until<=now
		case kind < 16:
			op.kind = opUnmute
			op.a, op.b = pickBy(), pickUser()
		case kind < 20:
			op.kind = opTakeMic
			op.a = pickUser()
		default:
			op.kind = opDropMic
			op.a = pickUser()
		}
		ops = append(ops, op)
	}
	return ops
}

// diffState 返回 Manager 与朴素模拟的状态差异，空串表示一致。
func diffState(v ontology.View, n *naive) string {
	if !reflect.DeepEqual(v.Roles, n.roles) {
		return fmt.Sprintf("roles: manager=%v naive=%v", v.Roles, n.roles)
	}
	if !reflect.DeepEqual(v.Mics, n.mics) {
		return fmt.Sprintf("mics: manager=%v naive=%v", v.Mics, n.mics)
	}
	if len(v.Queue) != 0 || len(n.queue) != 0 {
		if !reflect.DeepEqual(v.Queue, n.queue) {
			return fmt.Sprintf("queue: manager=%v naive=%v", v.Queue, n.queue)
		}
	}
	if !reflect.DeepEqual(v.Mutes, n.mutes) {
		return fmt.Sprintf("mutes: manager=%v naive=%v", v.Mutes, n.mutes)
	}
	return ""
}

// checkInvariants 校验被接受操作结束后的不变量。
func checkInvariants(t *testing.T, v ontology.View, now int64) {
	t.Helper()
	muted := func(u string) bool {
		r, ok := v.Mutes[u]
		return ok && now < r.Until
	}
	seen := make(map[string]int)
	empty := false
	for _, u := range v.Mics {
		if u == "" {
			empty = true
			continue
		}
		if muted(u) {
			t.Errorf("invariant: 麦上存在生效禁言者 %s", u)
		}
		seen[u]++
	}
	for _, u := range v.Queue {
		seen[u]++
		if empty && !muted(u) {
			t.Errorf("invariant: 有空麦且队列中 %s 未被禁言", u)
		}
	}
	for u, c := range seen {
		if c > 1 {
			t.Errorf("invariant: %s 出现在麦序中 %d 次", u, c)
		}
	}
	if len(v.Roles) > 0 {
		owners := 0
		for _, l := range v.Roles {
			if l == role.Owner {
				owners++
			}
		}
		if owners != 1 {
			t.Errorf("invariant: 房间非空时 Owner 数为 %d", owners)
		}
	}
}

// TestRandomAgainstNaive 用 1500 组随机操作序列对照朴素模拟，
// 日志打印输入、输出与判定依据；部分序列重放两次验证确定性。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		m := 1 + rng.Intn(4)
		ops := genSeq(rng, 40)
		mg := ontology.New(m)
		nv := newNaive(m)
		for step, op := range ops {
			gotErr := op.callManager(mg)
			wantErr, reason := op.callNaive(nv)
			t.Logf("seq=%d step=%d 输入=%s 输出=%v 判定依据=%s", seq, step, op, gotErr, reason)
			if gotErr != wantErr {
				t.Fatalf("seq=%d step=%d %s: manager err=%v, naive err=%v (%s)",
					seq, step, op, gotErr, wantErr, reason)
			}
			if d := diffState(mg.Snapshot(), nv); d != "" {
				t.Fatalf("seq=%d step=%d %s: state diff: %s", seq, step, op, d)
			}
			if gotErr == nil {
				checkInvariants(t, mg.Snapshot(), op.now)
			}
		}
		if seq%50 == 0 { // 相同操作序列重放结果相同
			replay := ontology.New(m)
			for _, op := range ops {
				_ = op.callManager(replay)
			}
			if !reflect.DeepEqual(mg.Snapshot(), replay.Snapshot()) {
				t.Fatalf("seq=%d: replay diverged", seq)
			}
		}
	}
}
