package room

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/seat"
)

// ---- 独立朴素模型：不依赖 room 的内部结构，按题意逐步演算 ----

const (
	nEmpty = iota
	nReserved
	nSeated
)

type nMem struct {
	name      string
	seat      int
	confirmed bool
}

type nHold struct {
	pid    string
	team   int
	expire int64
	mem    []nMem
}

type nRoom struct {
	t, s, d    int
	h          int64
	status     map[[2]int]int
	player     map[[2]int]string
	holds      map[string]*nHold
	standalone map[string][2]int // 预留到期后仍在座的已确认者
	banned     map[string]bool
	seq        int
	seqOf      map[string]int
	host       string
}

type naive struct {
	rooms    map[string]*nRoom
	parties  map[string][]string
	presence map[string]string // player -> rid
	maxNow   int64
	log      []string
}

func newNaive() *naive {
	return &naive{
		rooms:    map[string]*nRoom{},
		parties:  map[string][]string{},
		presence: map[string]string{},
	}
}

func (n *naive) say(f string, a ...any) { n.log = append(n.log, fmt.Sprintf(f, a...)) }

func badNow(now int64) bool { return now < 0 || now > 1_000_000_000_000 }

func (n *naive) newRoom(rid string, t, s, d int, h int64) error {
	if rid == "" || t < 2 || t > 8 || s < 1 || s > 16 || d < 1 || d > s || h < 1 || h > 1_000_000_000 {
		n.say("NewRoom(%v) => 参数非法", rid)
		return ErrInvalidParam
	}
	if _, ok := n.rooms[rid]; ok {
		n.say("NewRoom(%v) => 已存在", rid)
		return ErrRoomExists
	}
	r := &nRoom{
		t: t, s: s, d: d, h: h,
		status: map[[2]int]int{}, player: map[[2]int]string{},
		holds: map[string]*nHold{}, standalone: map[string][2]int{},
		banned: map[string]bool{}, seqOf: map[string]int{},
	}
	n.rooms[rid] = r
	n.say("NewRoom(%v T=%d S=%d D=%d H=%d) => ok", rid, t, s, d, h)
	return nil
}

func (n *naive) formParty(pid string, members []string) error {
	if pid == "" || len(members) < 1 || len(members) > 16 {
		return ErrInvalidParam
	}
	for _, name := range members {
		if name == "" {
			return ErrInvalidParam
		}
	}
	seen := map[string]bool{}
	for _, name := range members {
		if seen[name] {
			return ErrInvalidParam
		}
		seen[name] = true
	}
	if _, ok := n.parties[pid]; ok {
		return ErrPartyExists
	}
	for _, name := range members {
		if _, busy := n.presence2party(name); busy {
			return ErrMemberBusy
		}
	}
	cp := make([]string, len(members))
	copy(cp, members)
	n.parties[pid] = cp
	return nil
}

// presence2party 返回玩家当前所属且仍登记的组队（登记后不因离房消失）。
func (n *naive) presence2party(name string) (string, bool) {
	for pid, members := range n.parties {
		for _, m := range members {
			if m == name {
				return pid, true
			}
		}
	}
	return "", false
}

// expire 释放指定房间 expire<=now 预留中尚未确认的成员（取等失效）。
func (n *naive) expire(r *nRoom, now int64) int {
	released := 0
	for pid, hd := range r.holds {
		if hd.expire > now {
			continue
		}
		released++
		next := hd.mem[:0]
		for _, mm := range hd.mem {
			if mm.confirmed {
				r.standalone[mm.name] = [2]int{hd.team, mm.seat}
				continue
			}
			key := [2]int{hd.team, mm.seat}
			if r.status[key] == nReserved {
				delete(r.status, key)
				delete(r.player, key)
			}
			delete(n.presence, mm.name)
		}
		hd.mem = next
		delete(r.holds, pid)
	}
	if released > 0 {
		n.hostOf(r)
	}
	return released
}

func activeHolds(r *nRoom, now int64) map[string]*nHold {
	out := map[string]*nHold{}
	for pid, hd := range r.holds {
		if hd.expire > now {
			out[pid] = hd
		}
	}
	return out
}

// presentView 只读到期视角：玩家在指定房持有在座席位或未到期预留。
func (n *naive) presentView(rid, name string, now int64) bool {
	if n.presence[name] != rid {
		return false
	}
	if _, ok := n.rooms[rid].standalone[name]; ok {
		return true
	}
	// 已确认者在座恒有效，即使其 hold 已到期（惰性视角，尚未被 Seats 物化）。
	for _, hd := range n.rooms[rid].holds {
		for _, mm := range hd.mem {
			if mm.name == name && mm.confirmed {
				return true
			}
		}
	}
	for _, hd := range activeHolds(n.rooms[rid], now) {
		for _, mm := range hd.mem {
			if mm.name == name {
				return true
			}
		}
	}
	return false
}

// occupiedAny 只读视角：玩家在任一房间持有席位或有效预留。
func (n *naive) occupiedAny(name string, now int64) bool {
	for rid := range n.rooms {
		if n.presentView(rid, name, now) {
			return true
		}
	}
	return false
}

// expireOne 物化单个到期 hold。
func (n *naive) expireOne(r *nRoom, pid string, now int64) {
	hd, ok := r.holds[pid]
	if !ok || hd.expire > now {
		return
	}
	next := hd.mem[:0]
	for _, mm := range hd.mem {
		if mm.confirmed {
			r.standalone[mm.name] = [2]int{hd.team, mm.seat}
			next = append(next, mm)
			continue
		}
		key := [2]int{hd.team, mm.seat}
		if r.status[key] == nReserved {
			delete(r.status, key)
			delete(r.player, key)
		}
		delete(n.presence, mm.name)
	}
	hd.mem = next
	delete(r.holds, pid)
	n.hostOf(r)
}
func occView(r *nRoom, now int64) []int {
	c := make([]int, r.t)
	for key, st := range r.status {
		if st == nSeated {
			c[key[0]]++
		}
	}
	for _, hd := range activeHolds(r, now) {
		for _, mm := range hd.mem {
			if !mm.confirmed {
				c[hd.team]++
			}
		}
	}
	return c
}

func freeView(r *nRoom, team int, now int64) []int {
	used := map[int]bool{}
	for _, hd := range activeHolds(r, now) {
		if hd.team != team {
			continue
		}
		for _, mm := range hd.mem {
			if !mm.confirmed {
				used[mm.seat] = true
			}
		}
	}
	out := []int{}
	for idx := 0; idx < r.s; idx++ {
		key := [2]int{team, idx}
		if r.status[key] == nEmpty && !used[idx] {
			out = append(out, idx)
		}
	}
	return out
}

func (n *naive) reserve(now int64, rid, pid string) error {
	if rid == "" || pid == "" || badNow(now) {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockRollback
	}
	r, ok := n.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	members, ok := n.parties[pid]
	if !ok {
		return ErrPartyNotFound
	}
	// 全部判定以只读到期视角进行，物化只发生在成功落地时。
	for _, name := range members {
		if n.occupiedAny(name, now) {
			n.say("Reserve(now=%d %v %v) => 成员 %v 已占用", now, rid, pid, name)
			return ErrOccupied
		}
	}
	for _, name := range members {
		if r.banned[name] {
			n.say("Reserve(now=%d %v %v) => 成员 %v 被禁入", now, rid, pid, name)
			return ErrBanned
		}
	}
	c := occView(r, now)
	team := 0
	for tm := 1; tm < r.t; tm++ {
		if c[tm] < c[team] {
			team = tm
		}
	}
	free := freeView(r, team, now)
	if len(free) < len(members) {
		n.say("Reserve(now=%d %v %v) 占用=%v 选队=%d 空位=%d => 空席不足", now, rid, pid, c, team, len(free))
		return ErrNoSeats
	}
	lo, hi := c[team]+len(members), c[team]+len(members)
	for tm := 0; tm < r.t; tm++ {
		if tm == team {
			continue
		}
		if c[tm] < lo {
			lo = c[tm]
		}
		if c[tm] > hi {
			hi = c[tm]
		}
	}
	if hi-lo > r.d {
		n.say("Reserve(now=%d %v %v) 占用=%v 选队=%d 后差=%d => 失衡", now, rid, pid, c, team, hi-lo)
		return ErrImbalance
	}
	n.expire(r, now)
	// 成员在他房的到期未确认预留此刻一并物化释放。
	for _, name := range members {
		if or := n.presence[name]; or != "" && or != rid {
			if _, _, found := n.findMem(n.rooms[or], name); found {
				for pid, hd := range n.rooms[or].holds {
					for _, mm := range hd.mem {
						if mm.name == name && !mm.confirmed && hd.expire <= now {
							n.expireOne(n.rooms[or], pid, now)
						}
					}
				}
			}
		}
	}
	hd := &nHold{pid: pid, team: team, expire: now + r.h}
	for i, name := range members {
		idx := free[i]
		hd.mem = append(hd.mem, nMem{name: name, seat: idx})
		key := [2]int{team, idx}
		r.status[key] = nReserved
		r.player[key] = name
		n.presence[name] = rid
	}
	r.holds[pid] = hd
	n.maxNow = now
	n.say("Reserve(now=%d %v %v) 占用=%v 选队=%d 到期=%d => ok 席位=%v", now, rid, pid, c, team, hd.expire, free[:len(members)])
	return nil
}

// findMem 在本房 hold 中定位玩家。
func (n *naive) findMem(r *nRoom, name string) (*nHold, *nMem, bool) {
	for _, hd := range r.holds {
		for i := range hd.mem {
			if hd.mem[i].name == name {
				return hd, &hd.mem[i], true
			}
		}
	}
	return nil, nil, false
}

func (n *naive) hostOf(r *nRoom) {
	host, best := "", 0
	for key, st := range r.status {
		if st != nSeated {
			continue
		}
		name := r.player[key]
		sq, ok := r.seqOf[name]
		if !ok {
			continue
		}
		if host == "" || sq < best {
			host, best = name, sq
		}
	}
	r.host = host
}

func (n *naive) confirm(now int64, rid, player string) (int, error) {
	if rid == "" || player == "" || badNow(now) {
		return 0, ErrInvalidParam
	}
	if now < n.maxNow {
		return 0, ErrClockRollback
	}
	r, ok := n.rooms[rid]
	if !ok {
		return 0, ErrRoomNotFound
	}
	hd, mm, found := n.findMem(r, player)
	if !found || mm.confirmed || n.presence[player] != rid {
		n.say("Confirm(now=%d %v %v) => 无预留", now, rid, player)
		return 0, ErrNoReservation
	}
	// 只读视角：已到期预留不可确认。
	if hd.expire <= now {
		n.say("Confirm(now=%d %v %v) => 预留已到期", now, rid, player)
		return 0, ErrNoReservation
	}
	mm.confirmed = true
	r.status[[2]int{hd.team, mm.seat}] = nSeated
	r.seq++
	r.seqOf[player] = r.seq
	n.hostOf(r)
	n.maxNow = now
	n.say("Confirm(now=%d %v %v) => ok 序号=%d 房主=%q", now, rid, player, r.seq, r.host)
	return r.seq, nil
}

func (n *naive) removeSeat(r *nRoom, key [2]int, name string) {
	delete(r.status, key)
	delete(r.player, key)
	delete(n.presence, name)
	delete(r.seqOf, name)
	delete(r.standalone, name)
}

func (n *naive) remove(r *nRoom, hd *nHold, name string) {
	for i, mm := range hd.mem {
		if mm.name != name {
			continue
		}
		n.removeSeat(r, [2]int{hd.team, mm.seat}, name)
		hd.mem = append(hd.mem[:i], hd.mem[i+1:]...)
		if len(hd.mem) == 0 {
			delete(r.holds, hd.pid)
		}
		return
	}
}

func (n *naive) leave(now int64, rid, player string) error {
	if rid == "" || player == "" || badNow(now) {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockRollback
	}
	r, ok := n.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	if !n.presentView(rid, player, now) {
		return ErrNotInRoom
	}
	if key, isStandalone := r.standalone[player]; isStandalone {
		n.removeSeat(r, key, player)
	} else {
		hd, _, found := n.findMem(r, player)
		if !found {
			return ErrNotInRoom
		}
		if hd.expire <= now {
			n.expireOne(r, hd.pid, now)
			key, ok := r.standalone[player]
			if !ok {
				return ErrNotInRoom
			}
			n.removeSeat(r, key, player)
			n.hostOf(r)
			n.maxNow = now
			return nil
		}
		n.remove(r, hd, player)
	}
	n.hostOf(r)
	n.maxNow = now
	n.say("Leave(now=%d %v %v) => ok 房主=%q", now, rid, player, r.host)
	return nil
}

func (n *naive) kick(now int64, rid, by, target string) error {
	if rid == "" || by == "" || target == "" || by == target || badNow(now) {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockRollback
	}
	r, ok := n.rooms[rid]
	if !ok {
		return ErrRoomNotFound
	}
	if r.host != by {
		return ErrNotHost
	}
	if !n.presentView(rid, target, now) {
		return ErrNotInRoom
	}
	if key, isStandalone := r.standalone[target]; isStandalone {
		n.removeSeat(r, key, target)
	} else {
		hd, _, found := n.findMem(r, target)
		if !found {
			return ErrNotInRoom
		}
		if hd.expire <= now {
			n.expireOne(r, hd.pid, now)
			key, ok := r.standalone[target]
			if !ok {
				return ErrNotInRoom
			}
			n.removeSeat(r, key, target)
			r.banned[target] = true
			n.hostOf(r)
			n.maxNow = now
			return nil
		}
		n.remove(r, hd, target)
	}
	r.banned[target] = true
	n.hostOf(r)
	n.maxNow = now
	n.say("Kick(now=%d %v %v->%v) => ok 禁入 房主=%q", now, rid, by, target, r.host)
	return nil
}

type nSnapshot struct {
	status map[[2]int]int
	player map[[2]int]string
	host   string
}

func (n *naive) snapshot(now int64, rid string) (nSnapshot, error) {
	if rid == "" || badNow(now) {
		return nSnapshot{}, ErrInvalidParam
	}
	if now < n.maxNow {
		return nSnapshot{}, ErrClockRollback
	}
	r, ok := n.rooms[rid]
	if !ok {
		return nSnapshot{}, ErrRoomNotFound
	}
	n.expire(r, now)
	n.hostOf(r)
	n.maxNow = now
	st := map[[2]int]int{}
	for k, v := range r.status {
		st[k] = v
	}
	pl := map[[2]int]string{}
	for k, v := range r.player {
		pl[k] = v
	}
	return nSnapshot{status: st, player: pl, host: r.host}, nil
}

// ---- 随机序列：实现与朴素模型逐步对照 ----

type opKind int

const (
	kReserve opKind = iota
	kConfirm
	kLeave
	kKick
	kSeats
	kFormParty
)

type op struct {
	kind                         opKind
	now                          int64
	rid, pid, player, by, target string
	members                      []string
}

type roomCfg struct {
	rid     string
	t, s, d int
	h       int64
}

type scenario struct {
	rooms   []roomCfg
	parties map[string][]string
	players []string
	ops     []op
}

// lastNaive 供 verbose 抽样打印判定依据；测试串行执行，无需加锁。
var lastNaive *naive

func generateScenario(rng *rand.Rand) scenario {
	sc := scenario{parties: map[string][]string{}}
	nRooms := 1 + rng.Intn(3)
	rids := make([]string, 0, nRooms)
	for i := 0; i < nRooms; i++ {
		rid := fmt.Sprintf("R%d", i)
		rids = append(rids, rid)
		t := 2 + rng.Intn(3)
		s := 2 + rng.Intn(5)
		d := 1 + rng.Intn(s)
		h := int64(1 + rng.Intn(120))
		sc.rooms = append(sc.rooms, roomCfg{rid: rid, t: t, s: s, d: d, h: h})
	}
	nParties := 3 + rng.Intn(8)
	for i := 0; i < nParties; i++ {
		pid := fmt.Sprintf("P%d", i)
		size := 1 + rng.Intn(4)
		members := make([]string, size)
		for j := range members {
			members[j] = fmt.Sprintf("%s_m%d", pid, j)
		}
		sc.parties[pid] = members
		sc.players = append(sc.players, members...)
	}
	var now int64
	for i := 0; i < 160; i++ {
		if rng.Intn(2) == 0 {
			now += int64(rng.Intn(20))
		}
		rid := rids[rng.Intn(len(rids))]
		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			sc.ops = append(sc.ops, op{kind: kReserve, now: now, rid: rid, pid: fmt.Sprintf("P%d", rng.Intn(nParties))})
		case 4, 5:
			sc.ops = append(sc.ops, op{kind: kConfirm, now: now, rid: rid, player: sc.players[rng.Intn(len(sc.players))]})
		case 6:
			sc.ops = append(sc.ops, op{kind: kLeave, now: now, rid: rid, player: sc.players[rng.Intn(len(sc.players))]})
		case 7:
			by, tg := sc.players[rng.Intn(len(sc.players))], sc.players[rng.Intn(len(sc.players))]
			sc.ops = append(sc.ops, op{kind: kKick, now: now, rid: rid, by: by, target: tg})
		case 8:
			sc.ops = append(sc.ops, op{kind: kSeats, now: now, rid: rid})
		default:
			// 注入重复组队，检验拒绝而不破坏模型。
			pid := fmt.Sprintf("P%d", rng.Intn(nParties))
			sc.ops = append(sc.ops, op{kind: kFormParty, pid: pid, members: []string{pid + "_dup"}})
		}
	}
	return sc
}

func runScenario(t *testing.T, sc scenario, seed int64) {
	t.Helper()
	m := NewManager()
	n := newNaive()
	lastNaive = n
	for _, c := range sc.rooms {
		e1 := m.NewRoom(c.rid, c.t, c.s, c.d, c.h)
		e2 := n.newRoom(c.rid, c.t, c.s, c.d, c.h)
		if !sameErr(e1, e2) {
			t.Fatalf("seed=%d NewRoom %v: %v vs %v", seed, c, e1, e2)
		}
	}
	for pid, members := range sc.parties {
		e1 := m.FormParty(pid, members)
		e2 := n.formParty(pid, append([]string(nil), members...))
		if !sameErr(e1, e2) {
			t.Fatalf("seed=%d FormParty %v: %v vs %v", seed, pid, e1, e2)
		}
	}
	for step, o := range sc.ops {
		if testing.Verbose() && seed%100 == 0 {
			t.Logf("seed=%d step=%d 输入=%+v", seed, step, o)
		}
		var e1, e2 error
		var seq1, seq2 int
		switch o.kind {
		case kReserve:
			e1 = m.Reserve(o.now, o.rid, o.pid)
			e2 = n.reserve(o.now, o.rid, o.pid)
		case kConfirm:
			seq1, e1 = m.Confirm(o.now, o.rid, o.player)
			seq2, e2 = n.confirm(o.now, o.rid, o.player)
		case kLeave:
			e1 = m.Leave(o.now, o.rid, o.player)
			e2 = n.leave(o.now, o.rid, o.player)
		case kKick:
			e1 = m.Kick(o.now, o.rid, o.by, o.target)
			e2 = n.kick(o.now, o.rid, o.by, o.target)
		case kFormParty:
			e1 = m.FormParty(o.pid, o.members)
			e2 = n.formParty(o.pid, append([]string(nil), o.members...))
		case kSeats:
			var v1 *View
			v1, e1 = m.Seats(o.now, o.rid)
			var sn nSnapshot
			sn, e2 = n.snapshot(o.now, o.rid)
			if e1 == nil && e2 == nil {
				if diff := diffView(v1, sn); diff != "" {
					dumpAndFail(t, seed, step, o, n, "Seats 状态不一致: "+diff)
				}
			}
		}
		if !sameErr(e1, e2) || (o.kind == kConfirm && seq1 != seq2) {
			dumpAndFail(t, seed, step, o, n,
				fmt.Sprintf("返回不一致: impl=(%d,%v) naive=(%d,%v)", seq1, e1, seq2, e2))
		}
		if testing.Verbose() && seed%100 == 0 {
			t.Logf("seed=%d step=%d 输出: impl=(%d,%v) naive=(%d,%v)", seed, step, seq1, e1, seq2, e2)
		}
		if !checkImplInvariant(m) {
			t.Fatalf("seed=%d step=%d op=%+v impl 不变量被破坏\n%s", seed, step, o, joinLog(n))
		}
	}
	// 终态：所有房间取一个不回退的时刻做全量对照。
	for _, c := range sc.rooms {
		v1, e1 := m.Seats(n.maxNow, c.rid)
		sn, e2 := n.snapshot(n.maxNow, c.rid)
		if !sameErr(e1, e2) {
			t.Fatalf("seed=%d final %v err: %v vs %v", seed, c.rid, e1, e2)
		}
		if diff := diffView(v1, sn); diff != "" {
			t.Fatalf("seed=%d final %v %v\n%s", seed, c.rid, diff, joinLog(n))
		}
	}
	// 全局不变量：每名玩家至多在一个房间。
	count := map[string]int{}
	for _, c := range sc.rooms {
		v, _ := m.Seats(n.maxNow, c.rid)
		for _, cell := range v.Cells {
			if cell.Status != seat.Empty {
				count[cell.Player]++
			}
		}
	}
	for name, k := range count {
		if k > 1 {
			t.Fatalf("seed=%d player %s appears %d times", seed, name, k)
		}
	}
}

func checkImplInvariant(m *Manager) bool {
	count := map[string]int{}
	for rid, r := range m.rooms {
		for _, c := range r.table.Snapshot() {
			if c.Status != seat.Empty {
				count[c.Player+"@"+rid]++
			}
		}
	}
	for _, v := range count {
		if v > 1 {
			return false
		}
	}
	return true
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Error() == b.Error()
}

func diffView(v *View, sn nSnapshot) string {
	if v.Host != sn.host {
		return fmt.Sprintf("host impl=%q naive=%q", v.Host, sn.host)
	}
	for _, cell := range v.Cells {
		key := [2]int{cell.Team, cell.Index}
		wantSt := sn.status[key]
		gotSt := int(cell.Status)
		if gotSt != wantSt {
			return fmt.Sprintf("seat %v status impl=%d naive=%d", key, gotSt, wantSt)
		}
		if cell.Player != sn.player[key] {
			return fmt.Sprintf("seat %v player impl=%q naive=%q", key, cell.Player, sn.player[key])
		}
	}
	return ""
}

func dumpAndFail(t *testing.T, seed int64, step int, o op, n *naive, msg string) {
	t.Helper()
	t.Fatalf("seed=%d step=%d op=%+v\n%s\n判定日志:\n%s", seed, step, o, msg, joinLog(n))
}

func joinLog(n *naive) string {
	out := ""
	start := 0
	if len(n.log) > 40 {
		start = len(n.log) - 40
		out = fmt.Sprintf("...(省略前 %d 条)...\n", start)
	}
	for _, l := range n.log[start:] {
		out += "  " + l + "\n"
	}
	return out
}

func TestRandomSimulation1500(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sc := generateScenario(rng)
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			if testing.Verbose() && seed%100 == 0 {
				t.Logf("===== seed=%d 开始：%d 间房 %d 支组队 %d 名玩家 =====",
					seed, len(sc.rooms), len(sc.parties), len(sc.players))
			}
			runScenario(t, sc, seed)
			if testing.Verbose() && seed%100 == 0 {
				t.Logf("===== seed=%d 通过；最终判定日志（末 20 条）=====\n%s", seed, joinLog(lastNaive))
			}
		})
	}
}

// TestConcurrency 并发调用下结果等价某个串行序（go test -race 验证无数据竞争）。
func TestConcurrency(t *testing.T) {
	m := NewManager()
	if err := m.NewRoom("r", 4, 16, 16, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		pid := fmt.Sprintf("P%d", i)
		member := fmt.Sprintf("p%d", i)
		if err := m.FormParty(pid, []string{member}); err != nil {
			t.Fatal(err)
		}
		if err := m.Reserve(1, "r", pid); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = m.Confirm(2, "r", fmt.Sprintf("p%d", i))
			_, _ = m.Seats(2, "r")
		}(i)
	}
	wg.Wait()
	v, err := m.Seats(2, "r")
	if err != nil {
		t.Fatal(err)
	}
	seated := 0
	for _, c := range v.Cells {
		if c.Status == seat.Seated {
			seated++
		}
	}
	if seated != 32 {
		t.Fatalf("seated=%d want 32", seated)
	}
}
