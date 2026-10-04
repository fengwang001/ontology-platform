package room

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// ---------- 朴素逐步模拟 ----------
//
// 按题面规则直接编写：不做任何性能优化，到期用全量扫描落地，
// 选队、分位、房主移交均为线性扫描，用于对照被测实现。

type simHold struct {
	rid      string
	reserved bool
	expiry   int64
	gid      int
	team     int
	idx      int
}

type simRoom struct {
	t, s, d  int
	h        int64
	seats    [][]string
	banned   map[string]bool
	confSeq  map[string]int64
	nextConf int64
	owner    string
}

type sim struct {
	lastNow  int64
	clockSet bool
	rooms    map[string]*simRoom
	parties  map[string][]string
	bound    map[string]bool
	holds    map[string]*simHold
	nextGID  int
	released int    // 本次操作 sweep 释放的组数
	note     string // 判定依据（日志用）
}

func newSim() *sim {
	return &sim{
		rooms:   make(map[string]*simRoom),
		parties: make(map[string][]string),
		bound:   make(map[string]bool),
		holds:   make(map[string]*simHold),
	}
}

func (s *sim) valid(h *simHold, now int64) bool {
	return h != nil && (!h.reserved || h.expiry > now)
}

// sweep 全量扫描，释放全部 expiry <= now 的预留（仅未确认者）。
func (s *sim) sweep(now int64) {
	s.released = 0
	seen := make(map[int]bool)
	for player, h := range s.holds {
		if h.reserved && h.expiry <= now {
			rm := s.rooms[h.rid]
			rm.seats[h.team][h.idx] = ""
			delete(s.holds, player)
			if !seen[h.gid] {
				seen[h.gid] = true
				s.released++
			}
		}
	}
}

func (s *sim) checkClock(now int64) error {
	if s.clockSet && now < s.lastNow {
		return ErrClockSkew
	}
	return nil
}

func (s *sim) accept(now int64) {
	s.sweep(now)
	s.lastNow = now
	s.clockSet = true
}

func simValidMembers(members []string) bool {
	if len(members) < 1 || len(members) > 16 {
		return false
	}
	seen := make(map[string]bool, len(members))
	for _, name := range members {
		if name == "" || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func (s *sim) newRoom(rid string, t, ns, d int, h int64) error {
	if rid == "" || t < 2 || t > 8 || ns < 1 || ns > 16 || d < 1 || d > ns ||
		h < 1 || h > 1_000_000_000 {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if _, ok := s.rooms[rid]; ok {
		s.note = "rid 重复"
		return ErrRoomExists
	}
	seats := make([][]string, t)
	for i := range seats {
		seats[i] = make([]string, ns)
	}
	s.rooms[rid] = &simRoom{t: t, s: ns, d: d, h: h, seats: seats,
		banned: make(map[string]bool), confSeq: make(map[string]int64)}
	s.note = "ok"
	return nil
}

func (s *sim) formParty(pid string, members []string) error {
	if pid == "" || !simValidMembers(members) {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if _, ok := s.parties[pid]; ok {
		s.note = "pid 已存在"
		return ErrPartyExists
	}
	for _, name := range members {
		if s.bound[name] {
			s.note = fmt.Sprintf("成员 %s 已属其他组队", name)
			return ErrMemberBound
		}
	}
	cp := make([]string, len(members))
	copy(cp, members)
	s.parties[pid] = cp
	for _, name := range cp {
		s.bound[name] = true
	}
	s.note = "ok"
	return nil
}

func (s *sim) reserve(now int64, rid, pid string) error {
	if now < 0 || now > 1_000_000_000_000 || rid == "" || pid == "" {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		s.note = fmt.Sprintf("时钟回退: now=%d < last=%d", now, s.lastNow)
		return err
	}
	rm, ok := s.rooms[rid]
	if !ok {
		s.note = "房间不存在"
		return ErrRoomNotFound
	}
	members, ok := s.parties[pid]
	if !ok {
		s.note = "组队不存在"
		return ErrPartyNotFound
	}
	for _, name := range members {
		if s.valid(s.holds[name], now) {
			s.note = fmt.Sprintf("成员 %s 已持有席位或有效预留", name)
			return ErrBusy
		}
	}
	for _, name := range members {
		if rm.banned[name] {
			s.note = fmt.Sprintf("成员 %s 被禁入", name)
			return ErrBanned
		}
	}
	occ := make([]int, rm.t)
	for _, h := range s.holds {
		if h.rid == rid && s.valid(h, now) {
			occ[h.team]++
		}
	}
	team := 0
	for i := 1; i < rm.t; i++ {
		if occ[i] < occ[team] {
			team = i
		}
	}
	free := []int{}
	for idx := 0; idx < rm.s; idx++ {
		occName := rm.seats[team][idx]
		if occName == "" || !s.valid(s.holds[occName], now) {
			free = append(free, idx)
		}
	}
	n := len(members)
	if len(free) < n {
		s.note = fmt.Sprintf("队 %d 空位 %d < 组人数 %d", team, len(free), n)
		return ErrNoSeat
	}
	lo, hi := -1, -1
	for i, o := range occ {
		if i == team {
			o += n
		}
		if lo < 0 || o < lo {
			lo = o
		}
		if o > hi {
			hi = o
		}
	}
	if hi-lo > rm.d {
		s.note = fmt.Sprintf("失衡: 差 %d > D=%d", hi-lo, rm.d)
		return ErrImbalance
	}
	s.accept(now)
	s.nextGID++
	for i, name := range members {
		idx := free[i]
		rm.seats[team][idx] = name
		s.holds[name] = &simHold{rid: rid, reserved: true,
			expiry: now + rm.h, gid: s.nextGID, team: team, idx: idx}
	}
	s.note = fmt.Sprintf("ok: 队 %d 位 %v 到期 %d", team, free[:n], now+rm.h)
	return nil
}

func (s *sim) confirm(now int64, rid, player string) error {
	if now < 0 || now > 1_000_000_000_000 || rid == "" || player == "" {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		s.note = "时钟回退"
		return err
	}
	rm, ok := s.rooms[rid]
	if !ok {
		s.note = "房间不存在"
		return ErrRoomNotFound
	}
	h := s.holds[player]
	if h == nil || h.rid != rid || !h.reserved || h.expiry <= now {
		s.note = "无有效预留"
		return ErrNoReservation
	}
	s.accept(now)
	h.reserved = false
	rm.nextConf++
	rm.confSeq[player] = rm.nextConf
	if rm.owner == "" {
		rm.owner = player
	}
	s.note = fmt.Sprintf("ok: 序号 %d", rm.nextConf)
	return nil
}

func simOwner(rm *simRoom) string {
	best := ""
	var bestSeq int64
	for p, sq := range rm.confSeq {
		if best == "" || sq < bestSeq {
			best, bestSeq = p, sq
		}
	}
	return best
}

func (s *sim) leave(now int64, rid, player string) error {
	if now < 0 || now > 1_000_000_000_000 || rid == "" || player == "" {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		s.note = "时钟回退"
		return err
	}
	rm, ok := s.rooms[rid]
	if !ok {
		s.note = "房间不存在"
		return ErrRoomNotFound
	}
	h := s.holds[player]
	if h == nil || h.rid != rid || !s.valid(h, now) {
		s.note = "不在房间"
		return ErrNotInRoom
	}
	s.accept(now)
	rm.seats[h.team][h.idx] = ""
	delete(s.holds, player)
	if !h.reserved {
		delete(rm.confSeq, player)
	}
	if rm.owner == player {
		rm.owner = simOwner(rm)
	}
	s.note = "ok"
	return nil
}

func (s *sim) kick(now int64, rid, by, target string) error {
	if now < 0 || now > 1_000_000_000_000 || rid == "" || by == "" || target == "" || by == target {
		s.note = "参数非法"
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		s.note = "时钟回退"
		return err
	}
	rm, ok := s.rooms[rid]
	if !ok {
		s.note = "房间不存在"
		return ErrRoomNotFound
	}
	if rm.owner == "" || rm.owner != by {
		s.note = fmt.Sprintf("%s 不是房主", by)
		return ErrNotOwner
	}
	h := s.holds[target]
	if h == nil || h.rid != rid || !s.valid(h, now) {
		s.note = "目标不在房间"
		return ErrNotInRoom
	}
	s.accept(now)
	rm.seats[h.team][h.idx] = ""
	delete(s.holds, target)
	if !h.reserved {
		delete(rm.confSeq, target)
	}
	rm.banned[target] = true
	s.note = "ok"
	return nil
}

func (s *sim) seats(now int64, rid string) (RoomView, error) {
	if now < 0 || now > 1_000_000_000_000 || rid == "" {
		s.note = "参数非法"
		return RoomView{}, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		s.note = "时钟回退"
		return RoomView{}, err
	}
	rm, ok := s.rooms[rid]
	if !ok {
		s.note = "房间不存在"
		return RoomView{}, ErrRoomNotFound
	}
	s.accept(now)
	view := RoomView{Owner: rm.owner}
	for team := 0; team < rm.t; team++ {
		for idx := 0; idx < rm.s; idx++ {
			sv := SeatView{Team: team, Index: idx}
			if name := rm.seats[team][idx]; name != "" {
				sv.Occupant = name
				if s.holds[name].reserved {
					sv.State = Reserved
				} else {
					sv.State = Seated
				}
			}
			view.Seats = append(view.Seats, sv)
		}
	}
	s.note = "ok"
	return view, nil
}

// ---------- 随机操作序列生成 ----------

type genOp struct {
	kind    string
	now     int64
	rid     string
	pid     string
	player  string
	by      string
	target  string
	members []string
	t, s, d int
	h       int64
}

func (op genOp) String() string {
	switch op.kind {
	case "newroom":
		return fmt.Sprintf("NewRoom(%s,T=%d,S=%d,D=%d,H=%d)", op.rid, op.t, op.s, op.d, op.h)
	case "formparty":
		return fmt.Sprintf("FormParty(%s,%v)", op.pid, op.members)
	case "reserve":
		return fmt.Sprintf("Reserve(%d,%s,%s)", op.now, op.rid, op.pid)
	case "confirm":
		return fmt.Sprintf("Confirm(%d,%s,%s)", op.now, op.rid, op.player)
	case "leave":
		return fmt.Sprintf("Leave(%d,%s,%s)", op.now, op.rid, op.player)
	case "kick":
		return fmt.Sprintf("Kick(%d,%s,%s,%s)", op.now, op.rid, op.by, op.target)
	case "seats":
		return fmt.Sprintf("Seats(%d,%s)", op.now, op.rid)
	}
	return op.kind
}

var playerPool = []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"}

// genSequence 生成一条随机操作序列：房间参数、初始组队与 50 个随机操作。
// now 多数小步递增（配合小 H 制造到期与取等），偶尔原地、回退或越界。
func genSequence(rng *rand.Rand) []genOp {
	var ops []genOp
	nRooms := 1 + rng.Intn(3)
	for i := 0; i < nRooms; i++ {
		tm := 2 + rng.Intn(3)
		sz := 1 + rng.Intn(4)
		d := 1 + rng.Intn(sz)
		h := int64(1) << rng.Intn(7) // 1..64，小保留期便于命中到期
		ops = append(ops, genOp{kind: "newroom", rid: fmt.Sprintf("r%d", i),
			t: tm, s: sz, d: d, h: h})
	}
	nParties := 1 + rng.Intn(4)
	used := make(map[string]bool)
	memberParty := make(map[string]string)
	for i := 0; i < nParties; i++ {
		size := 1 + rng.Intn(3)
		var members []string
		for len(members) < size && len(used) < len(playerPool) {
			name := playerPool[rng.Intn(len(playerPool))]
			if !used[name] {
				used[name] = true
				members = append(members, name)
			}
		}
		if len(members) == 0 {
			continue
		}
		pid := fmt.Sprintf("g%d", i)
		for _, name := range members {
			memberParty[name] = pid
		}
		ops = append(ops, genOp{kind: "formparty", pid: fmt.Sprintf("g%d", i), members: members})
	}
	cursor := int64(0)
	extra := 0
	var confirmed []string
	partyMembers := make(map[string][]string)
	partyRoom := make(map[string]string)
	for _, op := range ops {
		if op.kind == "formparty" {
			partyMembers[op.pid] = op.members
		}
	}
	for i := 0; i < 50; i++ {
		switch rng.Intn(10) {
		case 0:
			cursor -= rng.Int63n(30) // 可能时钟回退甚至为负
		case 1:
			// 原地不动，制造 now 相等
		default:
			cursor += rng.Int63n(40)
		}
		now := cursor
		if rng.Intn(50) == 0 {
			now = 1_000_000_000_001 // 越界
		}
		rid := fmt.Sprintf("r%d", rng.Intn(nRooms))
		if rng.Intn(30) == 0 {
			rid = "rx"
		}
		pid := fmt.Sprintf("g%d", rng.Intn(nParties+1))
		player := playerPool[rng.Intn(len(playerPool))]
		op := genOp{now: now, rid: rid, pid: pid, player: player}
		switch w := rng.Intn(100); {
		case w < 35:
			op.kind = "reserve"
			if _, ok := partyMembers[pid]; ok {
				partyRoom[pid] = rid
			}
		case w < 55:
			op.kind = "confirm"
			if len(partyRoom) > 0 && rng.Intn(10) < 6 {
				// 定向：取预留过房间的组队成员，提高确认成功率
				pids := make([]string, 0, len(partyRoom))
				for p := range partyRoom {
					pids = append(pids, p)
				}
				pick := pids[rng.Intn(len(pids))]
				op.rid = partyRoom[pick]
				mem := partyMembers[pick]
				op.player = mem[rng.Intn(len(mem))]
			}
			confirmed = append(confirmed, op.player)
		case w < 70:
			op.kind = "leave"
		case w < 76:
			op.kind = "kick"
			if len(partyRoom) > 0 && rng.Intn(10) < 5 {
				// 定向：目标取某组队在本序列中预留过的房间成员
				pids := make([]string, 0, len(partyRoom))
				for p := range partyRoom {
					pids = append(pids, p)
				}
				pick := pids[rng.Intn(len(pids))]
				op.rid = partyRoom[pick]
				mem := partyMembers[pick]
				op.target = mem[rng.Intn(len(mem))]
			} else {
				op.target = playerPool[rng.Intn(len(playerPool))]
			}
			if len(confirmed) > 0 && rng.Intn(10) < 7 {
				op.by = confirmed[rng.Intn(len(confirmed))] // 更可能是房主
			} else {
				op.by = playerPool[rng.Intn(len(playerPool))]
			}
			if rng.Intn(10) == 0 {
				op.target = op.by // by == target，参数非法
			}
		case w < 90:
			op.kind = "seats"
		case w < 95:
			op.kind = "formparty"
			extra++
			op.pid = fmt.Sprintf("x%d", extra)
			size := 1 + rng.Intn(3)
			for j := 0; j < size; j++ {
				name := playerPool[rng.Intn(len(playerPool))]
				op.members = append(op.members, name)
				memberParty[name] = op.pid
			}
			partyMembers[op.pid] = op.members
		default:
			op.kind = "newroom"
			extra++
			op.rid = fmt.Sprintf("rx%d", extra)
			op.t = 1 + rng.Intn(9)
			op.s = rng.Intn(18)
			op.d = rng.Intn(18)
			op.h = int64(rng.Intn(200))
		}
		ops = append(ops, op)
		// 踢人后高概率追加同组再预留：命中"被踢者所在组再 Reserve 报禁入"。
		if op.kind == "kick" && rng.Intn(10) < 6 {
			if pid, ok := memberParty[op.target]; ok {
				ops = append(ops, genOp{kind: "reserve",
					now: op.now + rng.Int63n(5), rid: op.rid, pid: pid})
			}
		}
	}
	return ops
}

// execOps 在被测实现与朴素模拟上同步执行序列，逐步对照输出，
// 并校验 touched 上界。返回被测实现的输出签名（重放对照用）。
func execOps(t *testing.T, m *Manager, s *sim, ops []genOp, tag string) []string {
	t.Helper()
	var signature []string
	for i, op := range ops {
		var gotM, gotS error
		var viewM, viewS RoomView
		isSeats := false
		switch op.kind {
		case "newroom":
			gotM = m.NewRoom(op.rid, op.t, op.s, op.d, op.h)
			gotS = s.newRoom(op.rid, op.t, op.s, op.d, op.h)
		case "formparty":
			gotM = m.FormParty(op.pid, op.members)
			gotS = s.formParty(op.pid, op.members)
		case "reserve":
			gotM = m.Reserve(op.now, op.rid, op.pid)
			gotS = s.reserve(op.now, op.rid, op.pid)
		case "confirm":
			gotM = m.Confirm(op.now, op.rid, op.player)
			gotS = s.confirm(op.now, op.rid, op.player)
		case "leave":
			gotM = m.Leave(op.now, op.rid, op.player)
			gotS = s.leave(op.now, op.rid, op.player)
		case "kick":
			gotM = m.Kick(op.now, op.rid, op.by, op.target)
			gotS = s.kick(op.now, op.rid, op.by, op.target)
		case "seats":
			isSeats = true
			viewM, gotM = m.Seats(op.now, op.rid)
			viewS, gotS = s.seats(op.now, op.rid)
		}
		sig := fmt.Sprintf("err=%v", gotM)
		if isSeats && gotM == nil {
			sig = fmt.Sprintf("err=%v view=%+v", gotM, viewM)
		}
		signature = append(signature, sig)
		t.Logf("%s op#%d 输入=%s 输出=%v 判定依据=%s", tag, i, op, gotM, s.note)
		if gotM != gotS {
			t.Fatalf("%s op#%d 输入=%s: 被测=%v 模拟=%v", tag, i, op, gotM, gotS)
		}
		if isSeats && gotM == nil && !reflect.DeepEqual(viewM, viewS) {
			t.Fatalf("%s op#%d 输入=%s:\n被测视图 %+v\n模拟视图 %+v", tag, i, op, viewM, viewS)
		}
		bound := 0
		if gotM == nil {
			switch op.kind {
			case "reserve", "confirm", "leave", "kick", "seats":
				bound = s.released + 1
			}
		}
		if m.touched > bound {
			t.Fatalf("%s op#%d 输入=%s: touched=%d 超过 释放组数(%d)+1",
				tag, i, op, m.touched, s.released)
		}
	}
	return signature
}

// TestModelRandom 用 1500 组随机操作序列对照朴素逐步模拟；
// 每 100 组做一次同序列重放，验证结果可精确复现。
func TestModelRandom(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		ops := genSequence(rand.New(rand.NewSource(seed)))
		tag := fmt.Sprintf("seed=%d", seed)
		sig1 := execOps(t, NewManager(), newSim(), ops, tag)
		if seed%100 == 0 {
			if sig2 := execOps(t, NewManager(), newSim(), ops, tag+" 重放"); !reflect.DeepEqual(sig1, sig2) {
				t.Fatalf("seed=%d 重放结果不一致", seed)
			}
		}
	}
}

// TestTouchedScale 验证 touched 上界与房间数量无关：10 间与 10000 间对照。
func TestTouchedScale(t *testing.T) {
	var seqs [][]int
	for _, nRooms := range []int{10, 10000} {
		m := NewManager()
		s := newSim()
		for i := 0; i < nRooms; i++ {
			rid := fmt.Sprintf("r%d", i)
			if err := m.NewRoom(rid, 2, 4, 2, 50); err != nil {
				t.Fatal(err)
			}
			if err := s.newRoom(rid, 2, 4, 2, 50); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 20; i++ {
			pid := fmt.Sprintf("g%d", i)
			members := []string{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)}
			if err := m.FormParty(pid, members); err != nil {
				t.Fatal(err)
			}
			if err := s.formParty(pid, members); err != nil {
				t.Fatal(err)
			}
		}
		var touchedSeq []int
		gi := 0
		for now := int64(0); now <= 300; now += 7 {
			for k := 0; k < 2; k++ {
				gi++
				pid := fmt.Sprintf("g%d", gi%20)
				rid := fmt.Sprintf("r%d", gi%3)
				errM := m.Reserve(now, rid, pid)
				errS := s.reserve(now, rid, pid)
				if errM != errS {
					t.Fatalf("房间数=%d now=%d: 被测=%v 模拟=%v", nRooms, now, errM, errS)
				}
				if errM == nil && m.touched > s.released+1 {
					t.Fatalf("房间数=%d now=%d: touched=%d > 释放组数(%d)+1",
						nRooms, now, m.touched, s.released)
				}
				touchedSeq = append(touchedSeq, m.touched)
			}
			player := fmt.Sprintf("a%d", gi%20)
			errM := m.Confirm(now, "r0", player)
			errS := s.confirm(now, "r0", player)
			if errM != errS {
				t.Fatalf("房间数=%d now=%d confirm: 被测=%v 模拟=%v", nRooms, now, errM, errS)
			}
			touchedSeq = append(touchedSeq, m.touched)
		}
		seqs = append(seqs, touchedSeq)
	}
	if !reflect.DeepEqual(seqs[0], seqs[1]) {
		t.Fatalf("touched 序列不应随房间数变化:\n10 间:   %v\n10000 间: %v", seqs[0], seqs[1])
	}
}

// TestConcurrent 并发调用：结果等价于某串行顺序，且不变式保持。
func TestConcurrent(t *testing.T) {
	m := NewManager()
	for i := 0; i < 2; i++ {
		if err := m.NewRoom(fmt.Sprintf("r%d", i), 2, 4, 2, 50); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		if err := m.FormParty(fmt.Sprintf("g%d", i),
			[]string{fmt.Sprintf("p%d", i), fmt.Sprintf("q%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var cursor int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				now := atomic.AddInt64(&cursor, 1) - 1
				rid := fmt.Sprintf("r%d", rng.Intn(2))
				switch rng.Intn(4) {
				case 0:
					m.Reserve(now, rid, fmt.Sprintf("g%d", rng.Intn(8)))
				case 1:
					m.Confirm(now, rid, fmt.Sprintf("p%d", rng.Intn(8)))
				case 2:
					m.Leave(now, rid, fmt.Sprintf("q%d", rng.Intn(8)))
				case 3:
					m.Seats(now, rid)
				}
			}
		}(g)
	}
	wg.Wait()
	// 不变式：每名玩家至多一个席位；房主为空当且仅当无在座玩家。
	seen := make(map[string]string)
	for _, rid := range []string{"r0", "r1"} {
		view, err := m.Seats(1_000_000_000_000, rid)
		if err != nil {
			t.Fatal(err)
		}
		seated := 0
		for _, sv := range view.Seats {
			if sv.Occupant == "" {
				continue
			}
			if sv.State == Seated {
				seated++
			}
			if prev, dup := seen[sv.Occupant]; dup {
				t.Fatalf("玩家 %s 同时出现在房间 %s 与 %s", sv.Occupant, prev, rid)
			}
			seen[sv.Occupant] = rid
		}
		if (view.Owner == "") != (seated == 0) {
			t.Fatalf("房间 %s: 房主=%q 在座=%d，违反房主不变式", rid, view.Owner, seated)
		}
	}
}
