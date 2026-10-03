package version

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/authz"
	"ontology/lock"
)

// nVersion 是朴素模拟中的版本记录。
type nVersion struct {
	ver    uint64
	size   uint64
	marker bool
	mode   lock.Mode
	until  uint64
	hold   bool
	alive  bool
}

// naive 是按规则逐步写成的朴素模拟：线性扫描、独立实现判定逻辑。
type naive struct {
	mode    lock.Mode
	defRet  uint64
	lastNow uint64
	nextVer uint64
	vers    map[string][]*nVersion
	audit   []AuditEntry
}

func newNaive(mode lock.Mode, d uint64) *naive {
	return &naive{mode: mode, defRet: d, nextVer: 1, vers: map[string][]*nVersion{}}
}

func (n *naive) find(key string, ver uint64) *nVersion {
	for _, v := range n.vers[key] {
		if v.alive && v.ver == ver {
			return v
		}
	}
	return nil
}

func naiveCheckDelete(v *nVersion, bypass, canBypass bool, now uint64) ErrKind {
	if v.hold {
		return ErrLegalHold
	}
	if v.mode != lock.None && now < v.until {
		if v.mode == lock.Compliance {
			return ErrComplianceRetention
		}
		if !bypass || !canBypass {
			return ErrGovernanceRetention
		}
	}
	return noErr
}

func (n *naive) remove(key string, v *nVersion, now uint64) {
	if !v.marker && v.mode == lock.Governance && now < v.until {
		n.audit = append(n.audit, AuditEntry{Key: key, Ver: v.ver, Now: now, OldRetainUntil: v.until})
	}
	v.alive = false
}

func (n *naive) put(key string, size, now uint64) (uint64, ErrKind) {
	if key == "" || size > maxSize || now > maxNow {
		return 0, ErrInvalidParam
	}
	if now < n.lastNow {
		return 0, ErrClockRollback
	}
	v := &nVersion{ver: n.nextVer, size: size, alive: true}
	if n.defRet > 0 {
		v.mode = n.mode
		v.until = now + n.defRet
	}
	n.nextVer++
	n.vers[key] = append(n.vers[key], v)
	n.lastNow = now
	return v.ver, noErr
}

func (n *naive) del(key string, now uint64) (uint64, ErrKind) {
	if key == "" || now > maxNow {
		return 0, ErrInvalidParam
	}
	if now < n.lastNow {
		return 0, ErrClockRollback
	}
	v := &nVersion{ver: n.nextVer, marker: true, alive: true}
	n.nextVer++
	n.vers[key] = append(n.vers[key], v)
	n.lastNow = now
	return v.ver, noErr
}

func (n *naive) get(key string) (*Info, ErrKind) {
	if key == "" {
		return nil, ErrInvalidParam
	}
	var cur *nVersion
	for _, v := range n.vers[key] {
		if v.alive && (cur == nil || v.ver > cur.ver) {
			cur = v
		}
	}
	if cur == nil {
		return nil, ErrNotExist
	}
	if cur.marker {
		return nil, ErrMarkedDeleted
	}
	return &Info{Key: key, Ver: cur.ver, Size: cur.size, Mode: cur.mode, RetainUntil: cur.until, Hold: cur.hold}, noErr
}

func (n *naive) delVer(key string, ver uint64, bypass bool, now uint64, op authz.Set) ErrKind {
	if key == "" || ver == 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < n.lastNow {
		return ErrClockRollback
	}
	if !op.Has(authz.DeleteVersion) {
		return ErrNoPermission
	}
	v := n.find(key, ver)
	if v == nil {
		return ErrVersionNotFound
	}
	if !v.marker {
		if k := naiveCheckDelete(v, bypass, op.Has(authz.BypassGovernance), now); k != noErr {
			return k
		}
	}
	n.remove(key, v, now)
	n.lastNow = now
	return noErr
}

func (n *naive) setRet(key string, ver uint64, mode lock.Mode, until uint64, bypass bool, now uint64, op authz.Set) ErrKind {
	if key == "" || ver == 0 || now > maxNow || !mode.Valid() {
		return ErrInvalidParam
	}
	if mode != lock.None && (until <= now || until > maxUntil) {
		return ErrInvalidParam
	}
	if now < n.lastNow {
		return ErrClockRollback
	}
	if !op.Has(authz.PutRetention) {
		return ErrNoPermission
	}
	v := n.find(key, ver)
	if v == nil || v.marker {
		return ErrVersionNotFound
	}
	if v.mode != lock.None && now < v.until {
		if v.mode == lock.Compliance {
			if !(mode == lock.Compliance && until >= v.until) {
				return ErrComplianceRetention
			}
		} else {
			ok := (mode == lock.Governance || mode == lock.Compliance) && until >= v.until
			if !ok && !(bypass && op.Has(authz.BypassGovernance)) {
				return ErrGovernanceRetention
			}
		}
	}
	if mode == lock.None {
		v.mode, v.until = lock.None, 0
	} else {
		v.mode, v.until = mode, until
	}
	n.lastNow = now
	return noErr
}

func (n *naive) setHold(key string, ver uint64, on bool, now uint64, op authz.Set) ErrKind {
	if key == "" || ver == 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < n.lastNow {
		return ErrClockRollback
	}
	if !op.Has(authz.PutRetention) {
		return ErrNoPermission
	}
	v := n.find(key, ver)
	if v == nil || v.marker {
		return ErrVersionNotFound
	}
	v.hold = on
	n.lastNow = now
	return noErr
}

func (n *naive) batch(items []Item, bypass bool, now uint64, op authz.Set) (ErrKind, int) {
	if len(items) == 0 || len(items) > maxBatch || now > maxNow {
		return ErrInvalidParam, -1
	}
	seen := map[Item]bool{}
	for _, it := range items {
		if it.Key == "" || it.Ver == 0 || seen[it] {
			return ErrInvalidParam, -1
		}
		seen[it] = true
	}
	if now < n.lastNow {
		return ErrClockRollback, -1
	}
	if !op.Has(authz.DeleteVersion) {
		return ErrNoPermission, -1
	}
	vs := make([]*nVersion, len(items))
	for i, it := range items {
		v := n.find(it.Key, it.Ver)
		if v == nil {
			return ErrVersionNotFound, i
		}
		if !v.marker {
			if k := naiveCheckDelete(v, bypass, op.Has(authz.BypassGovernance), now); k != noErr {
				return k, i
			}
		}
		vs[i] = v
	}
	for i, it := range items {
		n.remove(it.Key, vs[i], now)
	}
	n.lastNow = now
	return noErr, -1
}

// snapshot 描述目标版本在判定前的锁状态，作为日志中的判定依据。
func (n *naive) snapshot(key string, ver uint64, now uint64) string {
	v := n.find(key, ver)
	if v == nil {
		return fmt.Sprintf("(%s,%d) 版本不存在", key, ver)
	}
	if v.marker {
		return fmt.Sprintf("(%s,%d) 删除标记，永远可删", key, ver)
	}
	active := v.mode != lock.None && now < v.until
	return fmt.Sprintf("(%s,%d) hold=%v mode=%d until=%d 生效=%v", key, ver, v.hold, v.mode, v.until, active)
}

// simOp 是一次随机生成的操作。
type simOp struct {
	kind   string
	key    string
	ver    uint64
	size   uint64
	now    uint64
	mode   lock.Mode
	until  uint64
	bypass bool
	on     bool
	perms  authz.Set
	items  []Item
	desc   string
}

// outcome 是一次操作的可比较结果。
type outcome struct {
	ver  uint64
	kind ErrKind
	idx  int
	info *Info
}

func (o outcome) equal(p outcome) bool {
	return o.ver == p.ver && o.kind == p.kind && o.idx == p.idx && reflect.DeepEqual(o.info, p.info)
}

func (o outcome) String() string {
	if o.kind != noErr {
		return fmt.Sprintf("拒绝(%v, 下标=%d)", o.kind, o.idx)
	}
	if o.info != nil {
		return fmt.Sprintf("Get->%+v", *o.info)
	}
	return fmt.Sprintf("成功(ver=%d)", o.ver)
}

var simKeys = []string{"a", "a", "b", "b", "c", "d", "e", ""}

type gen struct {
	r   *rand.Rand
	now uint64
}

func (g *gen) nextNow() uint64 {
	switch x := g.r.Intn(12); {
	case x < 8:
		g.now += uint64(g.r.Intn(4))
	case x < 11:
		if g.now > 0 {
			g.now -= uint64(g.r.Intn(3)) // 可能触发时钟回退
		}
	default:
		return 1_000_000_000_001 // now 越界
	}
	return g.now
}

func (g *gen) key() string { return simKeys[g.r.Intn(len(simKeys))] }

func (g *gen) perms() authz.Set {
	var s authz.Set
	if g.r.Intn(2) == 0 {
		s = s.With(authz.DeleteVersion)
	}
	if g.r.Intn(2) == 0 {
		s = s.With(authz.BypassGovernance)
	}
	if g.r.Intn(2) == 0 {
		s = s.With(authz.PutRetention)
	}
	return s
}

func (g *gen) ver(hint uint64) uint64 {
	return uint64(g.r.Intn(int(hint) + 3))
}

func (g *gen) makeOp(nv *naive) simOp {
	now := g.nextNow()
	perms := g.perms()
	bypass := g.r.Intn(2) == 0
	switch g.r.Intn(10) {
	case 0, 1, 2:
		size := uint64(g.r.Intn(1000))
		if g.r.Intn(20) == 0 {
			size = 1_000_000_000_000 + uint64(g.r.Intn(2))
		}
		key := g.key()
		return simOp{kind: "put", key: key, size: size, now: now, perms: perms,
			desc: fmt.Sprintf("Put(key=%q size=%d now=%d)", key, size, now)}
	case 3:
		key := g.key()
		return simOp{kind: "del", key: key, now: now, perms: perms,
			desc: fmt.Sprintf("Delete(key=%q now=%d)", key, now)}
	case 4:
		key := g.key()
		return simOp{kind: "get", key: key,
			desc: fmt.Sprintf("Get(key=%q)", key)}
	case 5, 6:
		key, ver := g.key(), g.ver(nv.nextVer)
		return simOp{kind: "delver", key: key, ver: ver, bypass: bypass, now: now, perms: perms,
			desc: fmt.Sprintf("DeleteVersion(key=%q ver=%d bypass=%v now=%d)", key, ver, bypass, now)}
	case 7:
		key, ver := g.key(), g.ver(nv.nextVer)
		modes := []lock.Mode{lock.None, lock.Governance, lock.Compliance, lock.Mode(9)}
		mode := modes[g.r.Intn(len(modes))]
		until := now + uint64(g.r.Intn(60)) - 2
		if g.r.Intn(20) == 0 {
			until = 1_000_000_000_001
		}
		return simOp{kind: "setret", key: key, ver: ver, mode: mode, until: until, bypass: bypass, now: now, perms: perms,
			desc: fmt.Sprintf("SetRetention(key=%q ver=%d mode=%d until=%d bypass=%v now=%d)", key, ver, mode, until, bypass, now)}
	case 8:
		key, ver := g.key(), g.ver(nv.nextVer)
		on := g.r.Intn(2) == 0
		return simOp{kind: "sethold", key: key, ver: ver, on: on, now: now, perms: perms,
			desc: fmt.Sprintf("SetHold(key=%q ver=%d on=%v now=%d)", key, ver, on, now)}
	default:
		cnt := g.r.Intn(5)
		items := make([]Item, cnt)
		for i := range items {
			items[i] = Item{Key: g.key(), Ver: g.ver(nv.nextVer)}
		}
		if cnt > 0 && g.r.Intn(4) == 0 {
			items[cnt-1] = items[0] // 批内重复
		}
		return simOp{kind: "batch", items: items, bypass: bypass, now: now, perms: perms,
			desc: fmt.Sprintf("DeleteVersions(items=%v bypass=%v now=%d)", items, bypass, now)}
	}
}

func applyBucket(b *Bucket, oc simOp) outcome {
	switch oc.kind {
	case "put":
		v, err := b.Put(oc.key, oc.size, oc.now, oc.perms)
		return outcome{ver: v, kind: errKindOf(err), idx: errIdxOf(err)}
	case "del":
		v, err := b.Delete(oc.key, oc.now, oc.perms)
		return outcome{ver: v, kind: errKindOf(err), idx: errIdxOf(err)}
	case "get":
		info, err := b.Get(oc.key)
		return outcome{kind: errKindOf(err), idx: errIdxOf(err), info: info}
	case "delver":
		err := b.DeleteVersion(oc.key, oc.ver, oc.bypass, oc.now, oc.perms)
		return outcome{kind: errKindOf(err), idx: errIdxOf(err)}
	case "setret":
		err := b.SetRetention(oc.key, oc.ver, oc.mode, oc.until, oc.bypass, oc.now, oc.perms)
		return outcome{kind: errKindOf(err), idx: errIdxOf(err)}
	case "sethold":
		err := b.SetHold(oc.key, oc.ver, oc.on, oc.now, oc.perms)
		return outcome{kind: errKindOf(err), idx: errIdxOf(err)}
	default:
		err := b.DeleteVersions(oc.items, oc.bypass, oc.now, oc.perms)
		return outcome{kind: errKindOf(err), idx: errIdxOf(err)}
	}
}

func applyNaive(nv *naive, oc simOp) outcome {
	switch oc.kind {
	case "put":
		v, k := nv.put(oc.key, oc.size, oc.now)
		return outcome{ver: v, kind: k, idx: -1}
	case "del":
		v, k := nv.del(oc.key, oc.now)
		return outcome{ver: v, kind: k, idx: -1}
	case "get":
		info, k := nv.get(oc.key)
		return outcome{kind: k, idx: -1, info: info}
	case "delver":
		return outcome{kind: nv.delVer(oc.key, oc.ver, oc.bypass, oc.now, oc.perms), idx: -1}
	case "setret":
		return outcome{kind: nv.setRet(oc.key, oc.ver, oc.mode, oc.until, oc.bypass, oc.now, oc.perms), idx: -1}
	case "sethold":
		return outcome{kind: nv.setHold(oc.key, oc.ver, oc.on, oc.now, oc.perms), idx: -1}
	default:
		k, idx := nv.batch(oc.items, oc.bypass, oc.now, oc.perms)
		return outcome{kind: k, idx: idx}
	}
}

func errKindOf(e *Error) ErrKind {
	if e == nil {
		return noErr
	}
	return e.Kind
}

func errIdxOf(e *Error) int {
	if e == nil {
		return -1
	}
	return e.Index
}

// TestRandomSimulation 用 1500 组随机操作序列对照朴素模拟，并验证重放一致性。
func TestRandomSimulation(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSim(t, seed)
		})
	}
}

func runSim(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	mode := lock.Governance
	var d uint64
	switch r.Intn(3) {
	case 1:
		mode = lock.Compliance
	case 2:
		if r.Intn(2) == 0 {
			mode = lock.Compliance
		}
		d = uint64(r.Intn(50))
	}
	b1, err := New(mode, d)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := New(mode, d)
	if err != nil {
		t.Fatal(err)
	}
	nv := newNaive(mode, d)
	g := &gen{r: r}
	t.Logf("桶配置: mode=%d D=%d", mode, d)
	steps := 20 + r.Intn(30)
	for i := 0; i < steps; i++ {
		oc := g.makeOp(nv)
		basis := ""
		switch oc.kind {
		case "delver", "setret", "sethold":
			basis = nv.snapshot(oc.key, oc.ver, oc.now)
		case "batch":
			for _, it := range oc.items {
				basis += nv.snapshot(it.Key, it.Ver, oc.now) + "; "
			}
		}
		out1 := applyBucket(b1, oc)
		outN := applyNaive(nv, oc)
		out2 := applyBucket(b2, oc)
		t.Logf("op %d: %s -> %s | 判定依据: %s", i, oc.desc, out1, basis)
		if !out1.equal(outN) {
			t.Fatalf("op %d %s: 实现=%s 朴素模拟=%s", i, oc.desc, out1, outN)
		}
		if !out1.equal(out2) {
			t.Fatalf("op %d %s: 重放不一致 %s vs %s", i, oc.desc, out1, out2)
		}
	}
	if a1, a2 := b1.Audit(), b2.Audit(); !reflect.DeepEqual(a1, a2) {
		t.Fatalf("重放审计不一致: %v vs %v", a1, a2)
	}
	if got, want := b1.Audit(), nv.audit; len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
		t.Fatalf("审计不一致: 实现=%v 朴素模拟=%v", got, want)
	}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		info1, err1 := b1.Get(k)
		infoN, errN := nv.get(k)
		if errKindOf(err1) != errN || !reflect.DeepEqual(info1, infoN) {
			t.Fatalf("最终 Get(%q) 不一致: 实现=(%+v,%v) 朴素模拟=(%+v,%v)", k, info1, err1, infoN, errN)
		}
	}
}
