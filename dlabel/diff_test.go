package dlabel

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// TestRandomDifferential 在大量随机生成的属性取值与操作序列上，
// 将生产实现与朴素参照实现逐项对照（结论、错误码、快照版本）。
func TestRandomDifferential(t *testing.T) {
	const trials = 40
	const opsPerTrial = 400
	for seed := int64(1); seed <= trials; seed++ {
		runDifferentialTrial(t, seed, opsPerTrial)
	}
}

type diffEnv struct {
	t       *testing.T
	rng     *rand.Rand
	p       *Platform
	nm      *naiveModel
	ot      string
	attrs   []string
	tags    []string // 已成功登记过的标签（可能已删除）
	users   []string
	insts   []string
	snaps   map[*Snapshot]int64 // 生产快照 -> 版本（朴素侧按版本读取）
	opCount int
}

func runDifferentialTrial(t *testing.T, seed int64, n int) {
	retained := int64(40)
	log := NewMemoryAuditLog(0)
	p := NewPlatform([]string{"root"}, WithAuditLog(log), WithRetainedVersions(retained))
	nm := newNaiveModel(retained)

	ot := "O"
	nAttrs := 6
	attrs := make([]string, nAttrs)
	kinds := map[string]ValueKind{}
	for i := range attrs {
		attrs[i] = fmt.Sprintf("a%d", i)
		kinds[attrs[i]] = KindInt
	}
	must(t, p.RegisterObjectType("root", ot, kinds))
	nm.registerType(ot, kinds)

	env := &diffEnv{
		t: t, rng: rand.New(rand.NewSource(seed)),
		p: p, nm: nm, ot: ot, attrs: attrs,
		users: []string{"u0", "u1", "u2", "u3"},
		snaps: map[*Snapshot]int64{},
	}
	// 初始实例
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("i%d", i)
		vals := env.randomValues(attrs)
		must(t, p.CreateInstance("root", ot, id, vals))
		if c := nm.write("root", ot, id, vals, true); c != CodeOK {
			t.Fatalf("seed=%d naive create failed: %v", seed, c)
		}
		env.insts = append(env.insts, id)
	}
	// 预置几条规则与授权，保证初始可读。
	env.seedBasePolicies()

	for env.opCount = 0; env.opCount < n; env.opCount++ {
		env.step(seed)
	}
}

func (e *diffEnv) seedBasePolicies() {
	// base 标签：a0 >= 0，恒携带（随机值取小范围非负）。
	body := AttrAtom("a0", OpGte, IntValue(0))
	must(e.t, e.p.SetRule("root", Rule{ObjectType: e.ot, Tag: "base", Body: body}))
	if c := e.nm.setRule(e.ot, []Rule{{ObjectType: e.ot, Tag: "base", Body: body}}); c != CodeOK {
		e.t.Fatal(c)
	}
	for _, u := range e.users {
		g := Grant{Subject: u, Tag: "base", Read: EffectAllow, Write: EffectAllow,
			Visibility: EffectAllow, Attrs: []string{"*"}}
		must(e.t, e.p.SetGrant("root", g))
		e.nm.setGrant(g)
	}
	e.tags = append(e.tags, "base")
}

func (e *diffEnv) randomValues(attrs []string) map[string]Value {
	out := map[string]Value{}
	for _, a := range attrs {
		out[a] = IntValue(int64(e.rng.Intn(20)))
	}
	return out
}

func (e *diffEnv) randomExpr() Expr {
	switch e.rng.Intn(6) {
	case 0:
		return ConstAtom(e.rng.Intn(2) == 0)
	case 1:
		return AttrAtom(e.attrs[e.rng.Intn(len(e.attrs))], OpIsNull, NullValue())
	case 2:
		attr := e.attrs[e.rng.Intn(len(e.attrs))]
		ops := []CompareOp{OpEq, OpNeq, OpLt, OpLte, OpGt, OpGte}
		return AttrAtom(attr, ops[e.rng.Intn(len(ops))], IntValue(int64(e.rng.Intn(20))))
	case 3:
		if len(e.tags) == 0 {
			return ConstAtom(true)
		}
		return TagAtom(e.tags[e.rng.Intn(len(e.tags))])
	case 4:
		return And(e.randomExpr(), e.randomExpr())
	default:
		return Or(e.randomExpr(), e.randomExpr())
	}
}

func (e *diffEnv) step(seed int64) {
	switch e.rng.Intn(14) {
	case 0, 1, 2: // root 写
		id := e.insts[e.rng.Intn(len(e.insts))]
		subset := e.randomAttrSubset()
		vals := map[string]Value{}
		for _, a := range subset {
			vals[a] = IntValue(int64(e.rng.Intn(20)))
		}
		err := e.p.WriteInstance("root", e.ot, id, vals)
		c := e.nm.write("root", e.ot, id, vals, false)
		e.expectCode(err, c, seed, "root write")
	case 3: // 用户写（可能被拒）
		id := e.insts[e.rng.Intn(len(e.insts))]
		u := e.users[e.rng.Intn(len(e.users))]
		subset := e.randomAttrSubset()
		vals := map[string]Value{}
		for _, a := range subset {
			vals[a] = IntValue(int64(e.rng.Intn(20)))
		}
		err := e.p.WriteInstance(u, e.ot, id, vals)
		c := e.nm.write(u, e.ot, id, vals, false)
		e.expectCode(err, c, seed, "user write")
	case 4, 5: // 新增/替换规则（含同批相互引用，可能产生缺失属性/循环/未知标签错误）
		e.randomRuleChange(seed)
	case 6: // 授权变更
		e.randomGrantChange()
	case 7: // 删除规则（可能被悬挂引用拒绝）
		if len(e.tags) > 1 {
			tag := e.tags[e.rng.Intn(len(e.tags))]
			err := e.p.DeleteRule("root", e.ot, tag)
			c := e.nm.deleteRule(e.ot, tag)
			e.expectCode(err, c, seed, "delete rule")
			if errorCode(err) == CodeOK {
				e.tags = removeString(e.tags, tag)
			}
		}
	case 8, 9: // 新快照读取
		e.snapshotRead(seed)
	case 10, 11: // 复用旧快照读取（可重复读 + 可能失效）
		e.oldSnapshotRead(seed)
	case 12: // 标签状态对照（管理员）
		id := e.insts[e.rng.Intn(len(e.insts))]
		if len(e.tags) > 0 {
			tag := e.tags[e.rng.Intn(len(e.tags))]
			snap := e.p.Begin("root")
			v := snap.Version()
			got, err := snap.TagCarried("root", e.ot, id, tag)
			want, c := e.nm.tagCarried(e.ot, id, tag, v)
			e.expectCode(err, c, seed, "tag carried")
			if err == nil && got != want {
				e.t.Fatalf("seed=%d op=%d tag %s carried: got %v want %v (v=%d)",
					seed, e.opCount, tag, got, want, v)
			}
		}
	case 13: // 释放快照
		for s := range e.snaps {
			s.Release()
			delete(e.snaps, s)
		}
	}
}

func (e *diffEnv) randomAttrSubset() []string {
	k := 1 + e.rng.Intn(len(e.attrs))
	perm := e.rng.Perm(len(e.attrs))[:k]
	out := make([]string, k)
	for i, idx := range perm {
		out[i] = e.attrs[idx]
	}
	return out
}

func (e *diffEnv) randomRuleChange(seed int64) {
	// 80% 正常新规则；10% 引用不存在属性；10% 构造一对循环规则。
	r := e.rng.Float64()
	if r < 0.1 {
		tag := e.freshTag()
		rule := Rule{ObjectType: e.ot, Tag: tag,
			Body: AttrAtom(fmt.Sprintf("ghost%d", e.rng.Intn(5)), OpEq, IntValue(1))}
		err := e.p.SetRule("root", rule)
		c := e.nm.setRule(e.ot, []Rule{rule})
		e.expectCode(err, c, seed, "missing-attr rule")
		if errorCode(err) == CodeOK {
			e.tags = append(e.tags, tag)
		}
		return
	}
	if r < 0.2 {
		t1, t2 := e.freshTag(), e.freshTag()+"b"
		rules := []Rule{
			{ObjectType: e.ot, Tag: t1, Body: TagAtom(t2)},
			{ObjectType: e.ot, Tag: t2, Body: TagAtom(t1)},
		}
		err := e.p.ReplaceRules("root", e.ot, rules)
		c := e.nm.setRule(e.ot, rules)
		e.expectCode(err, c, seed, "cyclic rules")
		if errorCode(err) == CodeOK {
			e.tags = append(e.tags, t1, t2)
		}
		return
	}
	tag := e.freshTag()
	rule := Rule{ObjectType: e.ot, Tag: tag, Body: e.randomExpr()}
	// 限制表达式规模，避免极深嵌套。
	err := e.p.SetRule("root", rule)
	c := e.nm.setRule(e.ot, []Rule{rule})
	e.expectCode(err, c, seed, "set rule")
	if errorCode(err) == CodeOK {
		e.tags = append(e.tags, tag)
	}
}

var tagCounter int

func (e *diffEnv) freshTag() string {
	tagCounter++
	return fmt.Sprintf("t%d_%d", e.opCount, tagCounter)
}

func (e *diffEnv) randomGrantChange() {
	if len(e.tags) == 0 {
		return
	}
	tag := e.tags[e.rng.Intn(len(e.tags))]
	u := e.users[e.rng.Intn(len(e.users))]
	eff := func() Effect {
		if e.rng.Intn(2) == 0 {
			return EffectDeny
		}
		return EffectAllow
	}
	attrs := []string{"*"}
	if e.rng.Intn(2) == 0 {
		attrs = e.randomAttrSubset()
	}
	g := Grant{Subject: u, Tag: tag, Read: eff(), Write: eff(), Visibility: eff(), Attrs: attrs}
	must(e.t, e.p.SetGrant("root", g))
	e.nm.setGrant(g)
}

func (e *diffEnv) snapshotRead(seed int64) {
	id := e.insts[e.rng.Intn(len(e.insts))]
	u := e.users[e.rng.Intn(len(e.users))]
	want := e.randomAttrSubset()
	snap := e.p.Begin(u)
	v := snap.Version()
	e.snaps[snap] = v
	res, err := snap.ReadAttributes(u, e.ot, id, want)
	ref := e.nm.read(u, e.ot, id, want, v)
	e.expectCode(err, ref.code, seed, "fresh snapshot read")
	if err == nil {
		e.compareRead(res, ref, seed)
	}
}

func (e *diffEnv) oldSnapshotRead(seed int64) {
	if len(e.snaps) == 0 {
		e.snapshotRead(seed)
		return
	}
	var snap *Snapshot
	var v int64
	for s, ver := range e.snaps {
		snap, v = s, ver
		break
	}
	id := e.insts[e.rng.Intn(len(e.insts))]
	u := e.users[e.rng.Intn(len(e.users))]
	want := e.randomAttrSubset()
	res, err := snap.ReadAttributes(u, e.ot, id, want)
	ref := e.nm.read(u, e.ot, id, want, v)
	e.expectCode(err, ref.code, seed, "old snapshot read")
	if err == nil {
		e.compareRead(res, ref, seed)
		if res.Version != v {
			e.t.Fatalf("seed=%d op=%d snapshot version drift: %d vs %d", seed, e.opCount, res.Version, v)
		}
	}
}

func (e *diffEnv) compareRead(res *ReadResult, ref naiveReadOutcome, seed int64) {
	attrs := make([]string, 0, len(ref.attrs))
	for a := range ref.attrs {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)
	for _, a := range attrs {
		got, ok := res.Attrs[a]
		if !ok {
			e.t.Fatalf("seed=%d op=%d attr %s missing from result", seed, e.opCount, a)
		}
		wantAllowed := ref.attrs[a] == 1
		if got.Allowed != wantAllowed {
			e.t.Fatalf("seed=%d op=%d attr %s allowed: got %v want %v",
				seed, e.opCount, a, got.Allowed, wantAllowed)
		}
		if wantAllowed {
			if !got.Value.Equal(ref.values[a]) {
				e.t.Fatalf("seed=%d op=%d attr %s value mismatch: %v vs %v",
					seed, e.opCount, a, got.Value, ref.values[a])
			}
		}
	}
}

func (e *diffEnv) expectCode(err error, want ErrorCode, seed int64, op string) {
	got := errorCode(err)
	if got != want {
		e.t.Fatalf("seed=%d op=%d %s: error code got %d (%v), want %d",
			seed, e.opCount, op, got, err, want)
	}
}

func removeString(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
