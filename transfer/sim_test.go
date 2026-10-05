package transfer_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/transfer"
)

// 朴素模拟：按规则逐步直写，允许全表扫描，用作被测系统的对照参照。

type simRes struct {
	owner  string
	expiry int64
}

type simTicket struct {
	dst       int64
	reqAt     int64
	returning bool
}

type simChar struct {
	shard    int64
	name     string
	lastName string
	pending  bool
	prev     int64
	hasPrev  bool
	prevDone int64
	cool     int64
	hasCool  bool
	blockers int
	tk       *simTicket
}

type sim struct {
	cd, r, u, wt int64
	maxNow       int64
	hasNow       bool
	caps         map[int64]int
	chars        map[string]*simChar
	holds        map[int64]map[string]string // sid -> name -> char
	resv         map[int64]map[string]simRes // sid -> name -> 保留
}

func newSim(cd, r, u, wt int64) *sim {
	return &sim{
		cd: cd, r: r, u: u, wt: wt,
		caps:  make(map[int64]int),
		chars: make(map[string]*simChar),
		holds: make(map[int64]map[string]string),
		resv:  make(map[int64]map[string]simRes),
	}
}

func (m *sim) validTicket(c *simChar, now int64) bool {
	return c.tk != nil && now < c.tk.reqAt+m.wt
}

// load 全表扫描统计：现有角色数 + 以 sid 为目的地的有效在途迁移单数。
func (m *sim) load(sid, now int64) int {
	n := 0
	for _, c := range m.chars {
		if c.shard == sid {
			n++
		}
		if m.validTicket(c, now) && c.tk.dst == sid {
			n++
		}
	}
	return n
}

func (m *sim) clockErr(now int64) error {
	if m.hasNow && now < m.maxNow {
		return transfer.ErrClockRollback
	}
	return nil
}

func (m *sim) accept(now int64) {
	if !m.hasNow || now > m.maxNow {
		m.maxNow, m.hasNow = now, true
	}
}

// nameErr 名字判定：被持有即占用；有效保留且保留者非本人即保留中。
func (m *sim) nameErr(sid int64, name, self string, now int64) error {
	if _, ok := m.holds[sid][name]; ok {
		return transfer.ErrNameOccupied
	}
	if res, ok := m.resv[sid][name]; ok && now < res.expiry && res.owner != self {
		return transfer.ErrNameReserved
	}
	return nil
}

func (m *sim) newShard(sid int64, cap int) (error, string) {
	if cap < 1 || cap > 1_000_000 {
		return transfer.ErrInvalidParam, "cap 越界"
	}
	if _, ok := m.caps[sid]; ok {
		return transfer.ErrShardExists, "服已存在"
	}
	m.caps[sid] = cap
	m.holds[sid] = make(map[string]string)
	m.resv[sid] = make(map[string]simRes)
	return nil, "ok"
}

func (m *sim) create(now, sid int64, id, name string) (error, string) {
	if now < 0 || now > 1_000_000_000_000 || id == "" || name == "" {
		return transfer.ErrInvalidParam, "参数非法"
	}
	if err := m.clockErr(now); err != nil {
		return err, fmt.Sprintf("时钟回退: now=%d < max=%d", now, m.maxNow)
	}
	if _, ok := m.caps[sid]; !ok {
		return transfer.ErrShardNotFound, "服不存在"
	}
	if _, ok := m.chars[id]; ok {
		return transfer.ErrCharExists, "角色已存在"
	}
	if l := m.load(sid, now); l >= m.caps[sid] {
		return transfer.ErrShardFull, fmt.Sprintf("负载 %d >= cap %d", l, m.caps[sid])
	}
	if err := m.nameErr(sid, name, id, now); err != nil {
		return err, "名字被占用或保留中"
	}
	m.holds[sid][name] = id
	delete(m.resv[sid], name)
	m.chars[id] = &simChar{shard: sid, name: name, lastName: name}
	m.accept(now)
	return nil, "ok"
}

func (m *sim) request(now int64, id string, dst int64) (error, string) {
	if now < 0 || now > 1_000_000_000_000 || id == "" {
		return transfer.ErrInvalidParam, "参数非法"
	}
	if err := m.clockErr(now); err != nil {
		return err, fmt.Sprintf("时钟回退: now=%d < max=%d", now, m.maxNow)
	}
	c, ok := m.chars[id]
	if !ok {
		return transfer.ErrCharNotFound, "角色不存在"
	}
	if _, ok := m.caps[dst]; !ok {
		return transfer.ErrShardNotFound, "服不存在"
	}
	if dst == c.shard {
		return transfer.ErrSameShard, "dst 即当前服"
	}
	if m.validTicket(c, now) {
		return transfer.ErrTicketExists, "已有有效迁移单"
	}
	if c.blockers > 0 {
		return transfer.ErrBlocked, fmt.Sprintf("阻断项=%d", c.blockers)
	}
	returning := c.hasPrev && dst == c.prev && now < c.prevDone+m.u
	if !returning && c.hasCool && now < c.cool+m.cd {
		return transfer.ErrCooling, fmt.Sprintf("now=%d < coolStart=%d+CD=%d", now, c.cool, m.cd)
	}
	if l := m.load(dst, now); l >= m.caps[dst] {
		return transfer.ErrShardFull, fmt.Sprintf("dst 负载 %d >= cap %d", l, m.caps[dst])
	}
	c.tk = &simTicket{dst: dst, reqAt: now, returning: returning}
	m.accept(now)
	return nil, fmt.Sprintf("ok dst=%d 回迁=%v", dst, returning)
}

func (m *sim) complete(now int64, id string) (error, string) {
	if now < 0 || now > 1_000_000_000_000 || id == "" {
		return transfer.ErrInvalidParam, "参数非法"
	}
	if err := m.clockErr(now); err != nil {
		return err, fmt.Sprintf("时钟回退: now=%d < max=%d", now, m.maxNow)
	}
	c, ok := m.chars[id]
	if !ok {
		return transfer.ErrCharNotFound, "角色不存在"
	}
	if !m.validTicket(c, now) {
		return transfer.ErrNoTicket, "无在途迁移"
	}
	tk := c.tk
	src := c.shard
	if c.name != "" {
		delete(m.holds[src], c.name)
		m.resv[src][c.name] = simRes{owner: id, expiry: now + m.r}
	}
	c.shard = tk.dst
	c.tk = nil
	got := false
	if desired := c.lastName; desired != "" && m.nameErr(tk.dst, desired, id, now) == nil {
		m.holds[tk.dst][desired] = id
		delete(m.resv[tk.dst], desired)
		c.name, c.pending, got = desired, false, true
	}
	if !got {
		c.name, c.pending = "", true
	}
	if tk.returning {
		c.hasPrev = false
	} else {
		c.prev, c.hasPrev, c.prevDone = src, true, now
		c.cool, c.hasCool = now, true
	}
	m.accept(now)
	return nil, fmt.Sprintf("ok %d->%d name=%q 回迁=%v", src, tk.dst, c.name, tk.returning)
}

func (m *sim) cancel(now int64, id string) (error, string) {
	if now < 0 || now > 1_000_000_000_000 || id == "" {
		return transfer.ErrInvalidParam, "参数非法"
	}
	if err := m.clockErr(now); err != nil {
		return err, fmt.Sprintf("时钟回退: now=%d < max=%d", now, m.maxNow)
	}
	c, ok := m.chars[id]
	if !ok {
		return transfer.ErrCharNotFound, "角色不存在"
	}
	if !m.validTicket(c, now) {
		return transfer.ErrNoTicket, "无在途迁移"
	}
	c.tk = nil
	m.accept(now)
	return nil, "ok"
}

func (m *sim) rename(now int64, id, name string) (error, string) {
	if now < 0 || now > 1_000_000_000_000 || id == "" || name == "" {
		return transfer.ErrInvalidParam, "参数非法"
	}
	if err := m.clockErr(now); err != nil {
		return err, fmt.Sprintf("时钟回退: now=%d < max=%d", now, m.maxNow)
	}
	c, ok := m.chars[id]
	if !ok {
		return transfer.ErrCharNotFound, "角色不存在"
	}
	if m.validTicket(c, now) {
		return transfer.ErrFrozen, "已冻结"
	}
	if err := m.nameErr(c.shard, name, id, now); err != nil {
		return err, "名字被占用或保留中"
	}
	if c.name != "" {
		delete(m.holds[c.shard], c.name)
	}
	m.holds[c.shard][name] = id
	delete(m.resv[c.shard], name)
	c.name, c.lastName, c.pending = name, name, false
	m.accept(now)
	return nil, "ok"
}

func (m *sim) setBlockers(id string, n int) (error, string) {
	if id == "" || n < 0 {
		return transfer.ErrInvalidParam, "参数非法"
	}
	c, ok := m.chars[id]
	if !ok {
		return transfer.ErrCharNotFound, "角色不存在"
	}
	c.blockers = n
	return nil, fmt.Sprintf("blockers=%d", n)
}

// rndOp 是随机序列中的一步操作。
type rndOp struct {
	kind string
	now  int64
	sid  int64
	cap  int
	char string
	name string
	dst  int64
	num  int
}

// genSeq 由种子确定性地生成参数与操作序列。
func genSeq(seed int64) (cd, r, u, wt int64, ops []rndOp) {
	rng := rand.New(rand.NewSource(seed))
	pick := func(xs ...int64) int64 { return xs[rng.Intn(len(xs))] }
	cd, r, u, wt = pick(1, 2, 5, 1000), pick(1, 2, 500), pick(1, 2, 200), pick(1, 2, 100)
	ops = append(ops,
		rndOp{kind: "newshard", sid: 1, cap: 2},
		rndOp{kind: "newshard", sid: 2, cap: 3},
		rndOp{kind: "newshard", sid: 3, cap: 1},
		rndOp{kind: "create", now: 0, sid: 1, char: "c0", name: "n0"},
		rndOp{kind: "create", now: 0, sid: 2, char: "c1", name: "n0"},
		rndOp{kind: "create", now: 0, sid: 2, char: "c2", name: "n1"},
	)
	m := newSim(cd, r, u, wt)
	for _, o := range ops {
		applySim(m, o)
	}
	now := int64(0)
	steps := []int64{0, 0, 0, 1, 2, 3, 10, 250, 1000}
	for i, n := 0, 30+rng.Intn(30); i < n; i++ {
		now += steps[rng.Intn(len(steps))]
		if rng.Intn(100) < 3 && now > 0 { // 小概率制造时钟回退
			now -= 1 + int64(rng.Intn(5))
			if now < 0 {
				now = 0
			}
		}
		var o rndOp
		if rng.Intn(100) < 35 {
			o, now = craftOp(m, rng, now, cd, u, wt)
		} else {
			o = randomOp(rng, now)
		}
		ops = append(ops, o)
		applySim(m, o) // 生成期同步推进模拟，供后续定向构造
	}
	return cd, r, u, wt, ops
}

// randomOp 生成一步随机操作。
func randomOp(rng *rand.Rand, now int64) rndOp {
	char := fmt.Sprintf("c%d", rng.Intn(8))
	name := fmt.Sprintf("n%d", rng.Intn(4))
	sid := int64(1 + rng.Intn(4))
	switch k := rng.Intn(100); {
	case k < 22:
		return rndOp{kind: "create", now: now, sid: sid, char: char, name: name}
	case k < 52:
		return rndOp{kind: "request", now: now, char: char, dst: sid}
	case k < 62:
		return rndOp{kind: "complete", now: now, char: char}
	case k < 68:
		return rndOp{kind: "cancel", now: now, char: char}
	case k < 84:
		return rndOp{kind: "rename", now: now, char: char, name: name}
	case k < 90:
		return rndOp{kind: "blockers", char: char, num: rng.Intn(3)}
	default:
		return rndOp{kind: "newshard", sid: int64(1 + rng.Intn(6)), cap: 1 + rng.Intn(4)}
	}
}

// craftOp 依据模拟状态定向构造边界操作：迁移单取等过期、回迁窗口取等、
// 冷却取等、保留期取等。返回操作与推进后的 now。
func craftOp(m *sim, rng *rand.Rand, now, cd, u, wt int64) (rndOp, int64) {
	var ids []string
	for id := range m.chars {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return randomOp(rng, now), now
	}
	for tries := 0; tries < 12; tries++ {
		id := ids[rng.Intn(len(ids))]
		c := m.chars[id]
		switch rng.Intn(4) {
		case 0: // 迁移单取等：reqAt+wt-1 有效，reqAt+wt 过期
			if c.tk != nil {
				at := c.tk.reqAt + wt - int64(rng.Intn(2))
				if at >= now {
					return rndOp{kind: "complete", now: at, char: id}, at
				}
			}
		case 1: // 回迁窗口取等：prevDone+u-1 回迁，prevDone+u 非回迁
			if c.hasPrev && !m.validTicket(c, now) {
				at := c.prevDone + u - int64(rng.Intn(2))
				if at >= now {
					return rndOp{kind: "request", now: at, char: id, dst: c.prev}, at
				}
			}
		case 2: // 冷却取等：coolStart+cd-1 冷却中，coolStart+cd 可迁
			if c.hasCool && !m.validTicket(c, now) {
				at := c.cool + cd - int64(rng.Intn(2))
				if at >= now {
					dst := c.shard%3 + 1
					if dst == c.shard {
						dst++
					}
					return rndOp{kind: "request", now: at, char: id, dst: dst}, at
				}
			}
		case 3: // 保留期取等：expiry-1 保留中，expiry 释放
			for sid, names := range m.resv {
				for name, res := range names {
					at := res.expiry - int64(rng.Intn(2))
					if at >= now {
						fresh := fmt.Sprintf("g%d", rng.Intn(1000000))
						return rndOp{kind: "create", now: at, sid: sid, char: fresh, name: name}, at
					}
				}
			}
		}
	}
	return randomOp(rng, now), now
}

func applySys(sys *transfer.System, o rndOp) error {
	switch o.kind {
	case "newshard":
		return sys.NewShard(o.sid, o.cap)
	case "create":
		return sys.Create(o.now, o.sid, o.char, o.name)
	case "request":
		return sys.Request(o.now, o.char, o.dst)
	case "complete":
		return sys.Complete(o.now, o.char)
	case "cancel":
		return sys.Cancel(o.now, o.char)
	case "rename":
		return sys.Rename(o.now, o.char, o.name)
	case "blockers":
		return sys.SetBlockers(o.char, o.num)
	}
	return nil
}

func applySim(m *sim, o rndOp) (error, string) {
	switch o.kind {
	case "newshard":
		return m.newShard(o.sid, o.cap)
	case "create":
		return m.create(o.now, o.sid, o.char, o.name)
	case "request":
		return m.request(o.now, o.char, o.dst)
	case "complete":
		return m.complete(o.now, o.char)
	case "cancel":
		return m.cancel(o.now, o.char)
	case "rename":
		return m.rename(o.now, o.char, o.name)
	case "blockers":
		return m.setBlockers(o.char, o.num)
	}
	return nil, "?"
}

func (o rndOp) String() string {
	return fmt.Sprintf("%s now=%d sid=%d cap=%d char=%q name=%q dst=%d n=%d",
		o.kind, o.now, o.sid, o.cap, o.char, o.name, o.dst, o.num)
}

// 1500 组随机操作序列与朴素模拟逐步对照，日志打印输入、输出与判定依据。
func TestRandomSim(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		cd, r, u, wt, ops := genSeq(seed)
		sys, err := transfer.New(cd, r, u, wt)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		m := newSim(cd, r, u, wt)
		t.Logf("seed=%d CD=%d R=%d U=%d Wt=%d ops=%d", seed, cd, r, u, wt, len(ops))
		for i, o := range ops {
			sysErr := applySys(sys, o)
			simErr, why := applySim(m, o)
			t.Logf("seed=%d op=%03d in=[%s] out=%v sim=%v why=%s", seed, i, o, sysErr, simErr, why)
			if sysErr != simErr {
				t.Fatalf("seed=%d op=%03d %s: sys=%v sim=%v (%s)", seed, i, o, sysErr, simErr, why)
			}
			for id, sc := range m.chars { // 可观察角色状态逐步一致
				view, ok := sys.Inspect(id)
				want := transfer.CharView{Shard: sc.shard, Name: sc.name, PendingRename: sc.pending}
				if !ok || view != want {
					t.Fatalf("seed=%d op=%03d %s: char %s sys=%+v(ok=%v) sim=%+v",
						seed, i, o, id, view, ok, want)
				}
			}
			if simErr == nil { // 被接受操作后负载视图一致
				for sid := range m.caps {
					if got, want := sys.ShardLoad(sid), m.load(sid, m.maxNow); got != want {
						t.Fatalf("seed=%d op=%03d %s: load(%d) sys=%d sim=%d",
							seed, i, o, sid, got, want)
					}
				}
			}
		}
	}
}

// 相同操作序列重放结果相同。
func TestReplayDeterministic(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		cd, r, u, wt, ops := genSeq(seed)
		var runs [2][]error
		for k := 0; k < 2; k++ {
			sys, err := transfer.New(cd, r, u, wt)
			if err != nil {
				t.Fatalf("seed=%d New: %v", seed, err)
			}
			for _, o := range ops {
				runs[k] = append(runs[k], applySys(sys, o))
			}
		}
		for i := range runs[0] {
			if runs[0][i] != runs[1][i] {
				t.Fatalf("seed=%d op=%03d %s: replay mismatch %v vs %v",
					seed, i, ops[i], runs[0][i], runs[1][i])
			}
		}
	}
}

// 并发冒烟：所有操作串行化，负载上界不被突破（配合 -race）。
func TestConcurrent(t *testing.T) {
	sys, err := transfer.New(5, 5, 5, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []int64{1, 2} {
		if err := sys.NewShard(sid, 4); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		if err := sys.Create(0, int64(1+i/4), fmt.Sprintf("c%d", i), fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 300; i++ {
				char := fmt.Sprintf("c%d", rng.Intn(8))
				switch rng.Intn(5) {
				case 0:
					sys.Request(0, char, int64(1+rng.Intn(2)))
				case 1:
					sys.Complete(0, char)
				case 2:
					sys.Cancel(0, char)
				case 3:
					sys.Rename(0, char, fmt.Sprintf("m%d", rng.Intn(8)))
				case 4:
					sys.SetBlockers(char, rng.Intn(2))
				}
			}
		}(g)
	}
	wg.Wait()
	for _, sid := range []int64{1, 2} {
		if got := sys.ShardLoad(sid); got > 4 {
			t.Fatalf("shard %d load %d exceeds cap 4", sid, got)
		}
	}
}
