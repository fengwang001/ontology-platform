package transfer

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// ---- 朴素逐步模型：每个判定都全量扫描，不共享实现代码 ----

type mOrder struct {
	src, dst      string
	start, expire int64
	isReturn      bool
}

type mChar struct {
	home     string
	name     string
	pending  bool
	prev     string
	prevDone int64
	coolSet  bool
	coolAt   int64
	order    *mOrder
}

type mName struct {
	holder   string
	reserver string
	expire   int64
}

type naive struct {
	cd, retain, win, ttl int64
	last                 int64
	caps                 map[string]int
	blockers             map[string]int
	chars                map[string]*mChar
	names                map[string]map[string]*mName // shard -> name
}

func newNaive(cd, r, u, wt int64) *naive {
	return &naive{
		cd: cd, retain: r, win: u, ttl: wt,
		caps:     map[string]int{},
		blockers: map[string]int{},
		chars:    map[string]*mChar{},
		names:    map[string]map[string]*mName{},
	}
}

func (m *naive) addShard(id string, cap int) {
	m.caps[id] = cap
	m.names[id] = map[string]*mName{}
}

func (m *naive) active(c *mChar, now int64) bool {
	return c.order != nil && now < c.order.expire
}

// effReserved 全量扫描所有角色的有效迁移单，朴素但显然正确。
func (m *naive) effReserved(dst string, now int64) int {
	n := 0
	for _, c := range m.chars {
		if m.active(c, now) && c.order.dst == dst {
			n++
		}
	}
	return n
}

func (m *naive) residentsAt(id string, now int64) int {
	n := 0
	for _, c := range m.chars {
		if c.home == id {
			n++
		}
	}
	return n
}

func (m *naive) nameCheck(now int64, shardID, char, name string) error {
	rec := m.names[shardID][name]
	if rec == nil {
		return nil
	}
	if rec.holder != "" && rec.holder != char {
		return ErrOccupied
	}
	if rec.reserver != "" && rec.reserver != char && now < rec.expire {
		return ErrNameReserved
	}
	return nil
}

func (m *naive) hold(now int64, shardID, char, name string) error {
	if err := m.nameCheck(now, shardID, char, name); err != nil {
		return err
	}
	rec := m.names[shardID][name]
	if rec == nil {
		rec = &mName{}
		m.names[shardID][name] = rec
	}
	rec.holder = char
	rec.reserver = ""
	rec.expire = 0
	return nil
}

func (m *naive) release(shardID, char, name string) {
	rec := m.names[shardID][name]
	if rec != nil && rec.holder == char {
		rec.holder = ""
	}
}

func (m *naive) detain(now int64, shardID, char, name string) {
	rec := m.names[shardID][name]
	if rec == nil {
		rec = &mName{}
		m.names[shardID][name] = rec
	}
	rec.holder = ""
	rec.reserver = char
	rec.expire = now + m.retain
}

func validStr(s string) bool { return s != "" }

func (m *naive) create(now int64, shardID, char, name string) error {
	if now < 0 || now > MaxNow || !validStr(shardID) || !validStr(char) || !validStr(name) {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRewind
	}
	if _, ok := m.caps[shardID]; !ok {
		return ErrNoShard
	}
	if _, ok := m.chars[char]; ok {
		return ErrCharExists
	}
	load := m.residentsAt(shardID, now) + m.effReserved(shardID, now)
	if load >= m.caps[shardID] {
		return ErrCapReached
	}
	if err := m.nameCheck(now, shardID, char, name); err != nil {
		return err
	}
	m.last = now
	if err := m.hold(now, shardID, char, name); err != nil {
		return err
	}
	m.chars[char] = &mChar{home: shardID, name: name}
	return nil
}

func (m *naive) request(now int64, char, dst string) error {
	if now < 0 || now > MaxNow || !validStr(char) || !validStr(dst) {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRewind
	}
	c, ok := m.chars[char]
	if !ok {
		return ErrNoChar
	}
	if _, ok := m.caps[dst]; !ok {
		return ErrNoShard
	}
	if c.home == dst {
		return ErrSameShard
	}
	if m.active(c, now) {
		return ErrActiveOrder
	}
	if m.blockers[char] > 0 {
		return ErrBlocked
	}
	isReturn := dst == c.prev && now < c.prevDone+m.win
	if !isReturn && c.coolSet && now < c.coolAt+m.cd {
		return ErrCoolingDown
	}
	load := m.residentsAt(dst, now) + m.effReserved(dst, now)
	if load >= m.caps[dst] {
		return ErrCapReached
	}
	m.last = now
	c.order = &mOrder{src: c.home, dst: dst, start: now, expire: now + m.ttl, isReturn: isReturn}
	return nil
}

func (m *naive) complete(now int64, char string) error {
	if now < 0 || now > MaxNow || !validStr(char) {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRewind
	}
	c, ok := m.chars[char]
	if !ok {
		return ErrNoChar
	}
	if !m.active(c, now) {
		return ErrNoOrder
	}
	m.last = now
	o := c.order
	if !c.pending {
		m.detain(now, o.src, char, c.name)
	}
	c.home = o.dst
	if err := m.hold(now, o.dst, char, c.name); err != nil {
		c.pending = true
	} else {
		c.pending = false
	}
	c.order = nil
	if o.isReturn {
		c.prev = ""
		c.prevDone = 0
	} else {
		c.prev = o.src
		c.prevDone = now
		c.coolSet = true
		c.coolAt = now
	}
	return nil
}

func (m *naive) cancel(now int64, char string) error {
	if now < 0 || now > MaxNow || !validStr(char) {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRewind
	}
	c, ok := m.chars[char]
	if !ok {
		return ErrNoChar
	}
	if !m.active(c, now) {
		return ErrNoOrder
	}
	m.last = now
	c.order = nil
	return nil
}

func (m *naive) rename(now int64, char, name string) error {
	if now < 0 || now > MaxNow || !validStr(char) || !validStr(name) {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRewind
	}
	c, ok := m.chars[char]
	if !ok {
		return ErrNoChar
	}
	if m.active(c, now) {
		return ErrFrozen
	}
	if err := m.nameCheck(now, c.home, char, name); err != nil {
		return err
	}
	m.last = now
	if !c.pending {
		m.release(c.home, char, c.name)
	}
	if err := m.hold(now, c.home, char, name); err != nil {
		c.pending = true
		return err
	}
	c.name = name
	c.pending = false
	return nil
}

// ---- 差分测试：1500 组随机序列，与朴素模型逐步对照 ----

type opKind int

const (
	opNewShard opKind = iota
	opCreate
	opRequest
	opComplete
	opCancel
	opRename
	opBlock
)

type op struct {
	kind opKind
	now  int64
	a, b string
	n    int
}

func errKey(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func compareStates(t *testing.T, s *System, m *naive, now int64, step int, log *strings.Builder) {
	t.Helper()
	// 角色集合与字段。
	if len(s.chars) != len(m.chars) {
		t.Fatalf("step %d char count sys=%d naive=%d\n%s", step, len(s.chars), len(m.chars), log.String())
	}
	for id, mc := range m.chars {
		sc := s.chars[id]
		if sc == nil {
			t.Fatalf("step %d char %s missing in sys\n%s", step, id, log.String())
		}
		sActive := s.orderActive(sc, now)
		mActive := m.active(mc, now)
		if sc.home != mc.home || sc.name != mc.name || sc.rename != mc.pending ||
			sc.prev != mc.prev || sc.prevDone != mc.prevDone ||
			sc.coolSet != mc.coolSet || sc.coolAt != mc.coolAt ||
			sActive != mActive {
			t.Fatalf("step %d char %s mismatch:\n sys=%+v active=%v\nnaive=%+v active=%v\n%s",
				step, id, sc, sActive, mc, mActive, log.String())
		}
		if sActive && mActive && (sc.order.IsReturn != mc.order.isReturn || sc.order.Dst != mc.order.dst ||
			sc.order.Src != mc.order.src || sc.order.Expire != mc.order.expire) {
			t.Fatalf("step %d order %s mismatch sys=%+v naive=%+v\n%s",
				step, id, sc.order, mc.order, log.String())
		}
	}
	// 每服常驻数与有效在途预留数。
	for id := range m.caps {
		sh, _ := s.shards.Get(id)
		sysRes := 0
		for _, sc := range s.chars {
			if sc.home == id {
				sysRes++
			}
		}
		if sh.Residents() != sysRes || sh.Residents() != m.residentsAt(id, now) {
			t.Fatalf("step %d shard %s residents sys=%d naive=%d\n%s",
				step, id, sh.Residents(), m.residentsAt(id, now), log.String())
		}
		sysEff := s.effReserved(id, now)
		naiveEff := m.effReserved(id, now)
		if sysEff != naiveEff {
			t.Fatalf("step %d shard %s effReserved sys=%d naive=%d\n%s",
				step, id, sysEff, naiveEff, log.String())
		}
		if sysEff+sh.Residents() > sh.Cap() {
			t.Fatalf("step %d shard %s load %d over cap %d\n%s",
				step, id, sysEff+sh.Residents(), sh.Cap(), log.String())
		}
	}
	// 名字持有者与有效保留。
	for sid, names := range m.names {
		for nm, mr := range names {
			sh := s.names.Holder(sid, nm)
			sr, sExpire := s.names.Reserver(sid, nm)
			mrEffHolder := mr.holder
			mrEffReserver := mr.reserver
			if mr.reserver != "" && now >= mr.expire {
				mrEffReserver = "" // 朴素模型保留记录但已失效
			}
			if sh != mrEffHolder {
				t.Fatalf("step %d (%s,%s) holder sys=%q naive=%q\n%s",
					step, sid, nm, sh, mrEffHolder, log.String())
			}
			// 系统侧保留惰性提交，比较“是否有效保留”。
			sysResActive := sr != "" && now < sExpire
			naiveResActive := mrEffReserver != ""
			if sysResActive != naiveResActive ||
				(sysResActive && sr != mrEffReserver) {
				t.Fatalf("step %d (%s,%s) reservation sys=%q naive=%q\n%s",
					step, sid, nm, sr, mrEffReserver, log.String())
			}
		}
	}
}

func basis(err error) string {
	switch err {
	case nil:
		return "接受"
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRewind:
		return "时钟回退"
	case ErrNoShard:
		return "服不存在"
	case ErrCharExists:
		return "角色已存在"
	case ErrNoChar:
		return "角色不存在"
	case ErrCapReached:
		return "负载已达cap"
	case ErrOccupied:
		return "名字被占用"
	case ErrNameReserved:
		return "名字保留中"
	case ErrSameShard:
		return "dst即当前服"
	case ErrActiveOrder:
		return "已有有效迁移单"
	case ErrBlocked:
		return "存在阻断项"
	case ErrCoolingDown:
		return "冷却中"
	case ErrNoOrder:
		return "无在途迁移"
	case ErrFrozen:
		return "已冻结"
	default:
		return "其他:" + err.Error()
	}
}

func TestDifferential1500(t *testing.T) {
	const sequences = 1500
	const maxOps = 60
	rng := rand.New(rand.NewSource(20261004))

	for seq := 0; seq < sequences; seq++ {
		cd := int64(rng.Intn(50) + 1)
		r := int64(rng.Intn(50) + 1)
		u := int64(rng.Intn(30) + 1)
		wt := int64(rng.Intn(20) + 1)
		sys, err := New(cd, r, u, wt)
		if err != nil {
			t.Fatal(err)
		}
		mdl := newNaive(cd, r, u, wt)

		nshards := rng.Intn(3) + 2
		shardIDs := make([]string, 0, nshards)
		for i := 0; i < nshards; i++ {
			id := strconv.Itoa(i + 1)
			capv := rng.Intn(4) + 1
			if e1 := sys.NewShard(id, capv); e1 != nil {
				t.Fatal(e1)
			}
			mdl.addShard(id, capv)
			shardIDs = append(shardIDs, id)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "== seq %d cd=%d R=%d U=%d Wt=%d shards=%v ==\n", seq, cd, r, u, wt, shardIDs)
		now := int64(0)
		knownChars := []string{}
		for step := 0; step < maxOps; step++ {
			// 时间：多数单调微增，偶尔回退、偶尔大跳（触发过期/保留到期）。
			switch rng.Intn(10) {
			case 0:
				now -= int64(rng.Intn(5) + 1)
				if now < 0 {
					now = 0
				}
			case 1:
				now += int64(rng.Intn(100) + 20)
			default:
				now += int64(rng.Intn(8))
			}

			var o op
			o.now = now
			kind := opKind(rng.Intn(7))
			o.kind = kind
			switch kind {
			case opNewShard:
				o.a = "s" + strconv.Itoa(rng.Intn(4))
				o.n = rng.Intn(3) + 1
			case opCreate:
				o.a = "C" + strconv.Itoa(rng.Intn(12))
				o.b = shardIDs[rng.Intn(len(shardIDs))]
				o.n = rng.Intn(5)
			case opRequest, opComplete, opCancel, opRename, opBlock:
				if len(knownChars) == 0 {
					o.a = "C" + strconv.Itoa(rng.Intn(12))
				} else {
					o.a = knownChars[rng.Intn(len(knownChars))]
				}
				if kind == opRequest {
					o.b = shardIDs[rng.Intn(len(shardIDs))]
				}
				if kind == opRename || kind == opCreate {
					o.n = rng.Intn(5)
				}
				if kind == opBlock {
					o.n = rng.Intn(3)
				}
			}
			name := "n" + strconv.Itoa(o.n)

			var e1, e2 error
			switch kind {
			case opNewShard:
				e1 = sys.NewShard(o.a, o.n)
				if _, ok := mdl.caps[o.a]; ok || o.a == "" || o.n < MinCap || o.n > MaxCap {
					e2 = ErrInvalidParam
				} else {
					mdl.addShard(o.a, o.n)
				}
			case opCreate:
				e1 = sys.Create(o.now, o.b, o.a, name)
				e2 = mdl.create(o.now, o.b, o.a, name)
				if e1 == nil {
					knownChars = appendIfMissing(knownChars, o.a)
				}
			case opRequest:
				e1 = sys.Request(o.now, o.a, o.b)
				e2 = mdl.request(o.now, o.a, o.b)
			case opComplete:
				e1 = sys.Complete(o.now, o.a)
				e2 = mdl.complete(o.now, o.a)
			case opCancel:
				e1 = sys.Cancel(o.now, o.a)
				e2 = mdl.cancel(o.now, o.a)
			case opRename:
				e1 = sys.Rename(o.now, o.a, name)
				e2 = mdl.rename(o.now, o.a, name)
			case opBlock:
				e1 = sys.SetBlockers(o.a, o.n)
				e2 = func() error {
					if o.a == "" || o.n < 0 {
						return ErrInvalidParam
					}
					if o.n == 0 {
						delete(mdl.blockers, o.a)
					} else {
						mdl.blockers[o.a] = o.n
					}
					return nil
				}()
			}

			fmt.Fprintf(&log, "step %d now=%d kind=%s a=%q b=%q n=%d -> sys[%s] naive[%s] (%s)\n",
				step, o.now, kindName(kind), o.a, o.b, o.n, errKey(e1), errKey(e2), basis(commonErr(e1, e2)))
			if errKey(e1) != errKey(e2) {
				t.Fatalf("seq %d step %d result mismatch:\n%s", seq, step, log.String())
			}
			// 可观察状态只在已接受时钟上推进；被拒绝操作的 now 不改变状态。
			compareStates(t, sys, mdl, sys.last, step, &log)
		}
		// 打印前两组完整日志作为“输入/输出/判定依据”证据。
		if seq < 2 {
			t.Logf("\n%s", log.String())
		}
	}
}

func commonErr(e1, e2 error) error {
	if e1 != nil {
		return e1
	}
	return e2
}

func appendIfMissing(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

func kindName(k opKind) string {
	switch k {
	case opNewShard:
		return "NewShard"
	case opCreate:
		return "Create"
	case opRequest:
		return "Request"
	case opComplete:
		return "Complete"
	case opCancel:
		return "Cancel"
	case opRename:
		return "Rename"
	case opBlock:
		return "SetBlockers"
	default:
		return "?"
	}
}
