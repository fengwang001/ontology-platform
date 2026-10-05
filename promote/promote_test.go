package promote_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/attest"
	"ontology/promote"
	"ontology/registry"
)

func specStages() []registry.Stage {
	return []registry.Stage{
		{Name: "dev"},
		{Name: "staging", Immutable: true, S: 60, Required: []string{"test"}, Trusted: []string{"ci"}},
		{Name: "release", Immutable: true, S: 3600, Required: []string{"test", "scan"}, Trusted: []string{"ci", "sec"}},
	}
}

func allPerms(stages ...string) registry.Caller {
	c := registry.Caller{}
	for _, s := range stages {
		for _, a := range []registry.Action{registry.Push, registry.Promote, registry.Yank, registry.Alias} {
			c[registry.Permission{Action: a, Stage: s}] = true
		}
	}
	return c
}

type system struct {
	repo  *registry.Repo
	store *attest.Store
	gate  *promote.Gate
}

func newSystem(t *testing.T, stages []registry.Stage) *system {
	t.Helper()
	r, err := registry.New(stages)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	st := attest.New(r)
	return &system{repo: r, store: st, gate: promote.New(r, st)}
}

// step 是表驱动操作；op 为 push/attest/revoke/promote/yank/alias/resolve。
type step struct {
	op      string
	now     int64
	stage   string // yank/alias/resolve 的级；promote 的 from 级
	name    string
	tag     string
	digest  string
	typ     string
	signer  string
	exp     int64
	caller  registry.Caller
	want    error
	wantDig string
}

func (s *system) run(t *testing.T, steps []step) {
	t.Helper()
	for i, st := range steps {
		if st.name == "" {
			st.name = "app"
		}
		if st.tag == "" {
			st.tag = "v1"
		}
		var err error
		switch st.op {
		case "push":
			err = s.repo.Push(st.now, st.name, st.tag, st.digest, st.caller)
		case "attest":
			err = s.store.Attest(st.now, st.digest, st.typ, st.signer, st.exp)
		case "revoke":
			err = s.store.RevokeSigner(st.now, st.signer)
		case "promote":
			err = s.gate.Promote(st.now, st.name, st.tag, st.stage, st.caller)
		case "yank":
			err = s.repo.Yank(st.now, st.stage, st.name, st.tag, st.caller)
		case "alias":
			err = s.repo.SetAlias(st.now, st.stage, st.name, st.tag, st.digest, st.caller)
		case "resolve":
			var got string
			got, err = s.repo.Resolve(st.stage, st.name, st.tag)
			if err == nil && got != st.wantDig {
				t.Fatalf("step %d resolve: got %q, want %q", i, got, st.wantDig)
			}
		default:
			t.Fatalf("step %d: unknown op %q", i, st.op)
		}
		if st.want == nil {
			if err != nil {
				t.Fatalf("step %d %s: got err %v, want nil", i, st.op, err)
			}
		} else if !errors.Is(err, st.want) {
			t.Fatalf("step %d %s: got err %v, want %v", i, st.op, err, st.want)
		}
	}
}

// TestSpecExample 完整复现题目三级示例。
func TestSpecExample(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 10, digest: "d1", typ: "test", signer: "ci", exp: 5000},
		{op: "promote", now: 59, stage: "dev", caller: full, want: registry.ErrDwell},
		{op: "promote", now: 60, stage: "dev", caller: full}, // 驻留恰等通过
		{op: "resolve", stage: "staging", wantDig: "d1"},
		{op: "push", now: 100, digest: "d2", caller: full},                                   // 覆盖 dev
		{op: "promote", now: 200, stage: "dev", caller: full, want: registry.ErrAttestation}, // d2 无 test 证明
		{op: "attest", now: 210, digest: "d2", typ: "test", signer: "ci", exp: 9000},
		{op: "promote", now: 300, stage: "dev", caller: full, want: registry.ErrImmutable},        // staging v1 已指向 d1
		{op: "promote", now: 3659, stage: "staging", caller: full, want: registry.ErrDwell},       // 3599 < 3600
		{op: "promote", now: 3660, stage: "staging", caller: full, want: registry.ErrAttestation}, // 缺 scan
		{op: "attest", now: 3700, digest: "d1", typ: "scan", signer: "sec", exp: 4000},
		{op: "promote", now: 3999, stage: "staging", caller: full},
		{op: "resolve", stage: "release", wantDig: "d1"},
		// 重放：d1 已在 release，但 scan 证明在 t=4000 恰到期，须重新通过全部检查。
		{op: "promote", now: 4000, stage: "staging", caller: full, want: registry.ErrAttestation},
	})
	// first(staging, app, d1) 应为 60。
	if got, ok := firstOf(s.repo, "staging", "app", "d1"); !ok || got != 60 {
		t.Fatalf("first(staging,app,d1) = %d, %v; want 60, true", got, ok)
	}
}

func firstOf(r *registry.Repo, stage, name, digest string) (int64, bool) {
	r.Lock()
	defer r.Unlock()
	return r.FirstLocked(stage, name, digest)
}

// TestDwellBoundary 驻留恰等通过、差 1 拒绝。
func TestDwellBoundary(t *testing.T) {
	full := allPerms("dev", "staging")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 100, digest: "d1", caller: full},
		{op: "attest", now: 101, digest: "d1", typ: "test", signer: "ci", exp: 1000},
		{op: "promote", now: 159, stage: "dev", caller: full, want: registry.ErrDwell},
		{op: "promote", now: 160, stage: "dev", caller: full},
	})
}

// TestFirstNotRefreshedByRepush 重复进入不刷新 first。
func TestFirstNotRefreshedByRepush(t *testing.T) {
	full := allPerms("dev", "staging")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "push", now: 10, digest: "d2", caller: full},
		{op: "push", now: 20, digest: "d1", caller: full}, // d1 重新进入 dev，first 仍为 0
		{op: "attest", now: 25, digest: "d1", typ: "test", signer: "ci", exp: 1000},
		{op: "promote", now: 60, stage: "dev", caller: full}, // 若 first 被刷新为 20 则驻留仅 40
	})
	if got, _ := firstOf(s.repo, "dev", "app", "d1"); got != 0 {
		t.Fatalf("first(dev,app,d1) = %d, want 0", got)
	}
}

// TestAttestationExactExpiry 证明恰到期失效。
func TestAttestationExactExpiry(t *testing.T) {
	full := allPerms("dev", "staging")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 100},
		{op: "promote", now: 100, stage: "dev", caller: full, want: registry.ErrAttestation}, // t==exp 失效
		{op: "promote", now: 99, stage: "dev", caller: full},                                 // 被拒操作不推进时钟，99 仍合法且证明有效
		{op: "resolve", stage: "staging", wantDig: "d1"},
	})
}

// TestSignerNotTrusted 签名者不在目标级 trusted 集合。
func TestSignerNotTrusted(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "sec", exp: 10000}, // sec 不在 staging 的 trusted
		{op: "promote", now: 60, stage: "dev", caller: full, want: registry.ErrAttestation},
		{op: "attest", now: 61, digest: "d1", typ: "test", signer: "ci", exp: 10000},
		{op: "promote", now: 62, stage: "dev", caller: full},
	})
}

// TestMissingTypeOrder 缺失类型按 required 次序报第一个。
func TestMissingTypeOrder(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	missing := func(s *system, now int64, from string) string {
		t.Helper()
		err := s.gate.Promote(now, "app", "v1", from, full)
		if !errors.Is(err, registry.ErrAttestation) {
			t.Fatalf("got %v, want ErrAttestation", err)
		}
		return strings.TrimPrefix(err.Error(), registry.ErrAttestation.Error()+": ")
	}

	// staging 的 required=[test]：只有 scan 证明时报 test。
	s1 := newSystem(t, specStages())
	s1.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "scan", signer: "sec", exp: 10000},
	})
	if got := missing(s1, 60, "dev"); got != "test" {
		t.Fatalf("missing type = %q, want test", got)
	}

	// release 的 required=[test,scan]：test 已过期、scan 有效时报 test（次序中的第一个）。
	s2 := newSystem(t, specStages())
	s2.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 100},
		{op: "attest", now: 2, digest: "d1", typ: "scan", signer: "sec", exp: 10000},
		{op: "promote", now: 60, stage: "dev", caller: full}, // test 在 60 时仍有效，进入 staging
	})
	if got := missing(s2, 3660, "staging"); got != "test" {
		t.Fatalf("missing type = %q, want test", got)
	}

	// test 有效、缺 scan 时报 scan。
	s3 := newSystem(t, specStages())
	s3.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 10000},
		{op: "promote", now: 60, stage: "dev", caller: full},
	})
	if got := missing(s3, 3660, "staging"); got != "scan" {
		t.Fatalf("missing type = %q, want scan", got)
	}
}

// TestIdempotentReplay 幂等重放：全部检查重新执行。
func TestIdempotentReplay(t *testing.T) {
	full := allPerms("dev", "staging")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 200},
		{op: "promote", now: 60, stage: "dev", caller: full},
		{op: "promote", now: 61, stage: "dev", caller: full},                                 // 幂等重放成功
		{op: "promote", now: 200, stage: "dev", caller: full, want: registry.ErrAttestation}, // 证明过期后重放被拒
	})
}

// TestTombstoneRejectsSameDigest 墓碑对同摘要也拒绝；可变级撤回后可重用。
func TestTombstoneRejectsSameDigest(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 10000},
		{op: "attest", now: 2, digest: "d1", typ: "scan", signer: "sec", exp: 10000},
		{op: "promote", now: 60, stage: "dev", caller: full},
		{op: "promote", now: 3660, stage: "staging", caller: full},
		{op: "yank", now: 3700, stage: "release", caller: full},
		// 同一摘要 d1 再晋到 release 的 v1：墓碑拒绝。
		{op: "promote", now: 3701, stage: "staging", caller: full, want: registry.ErrYanked},
		// dev 上 yank 后同名标签可重推。
		{op: "yank", now: 3702, stage: "dev", caller: full},
		{op: "push", now: 3703, digest: "d9", caller: full},
		{op: "resolve", stage: "dev", wantDig: "d9"},
	})
}

// TestAliasTagConflictBothWays 别名与标签双向冲突。
func TestAliasTagConflictBothWays(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 10000},
		{op: "promote", now: 60, stage: "dev", caller: full},
		// release 上别名指向 v1 后，v1 不可撤回。
		{op: "promote", now: 3660, stage: "staging", caller: full, want: registry.ErrAttestation}, // 缺 scan
		{op: "attest", now: 3661, digest: "d1", typ: "scan", signer: "sec", exp: 10000},
		{op: "promote", now: 3662, stage: "staging", caller: full},
		{op: "alias", now: 3663, stage: "release", tag: "stable", digest: "v1", caller: full},
		{op: "yank", now: 3664, stage: "release", caller: full, want: registry.ErrReferenced},
		// staging 上别名 v2 存在时，把 dev 的 v2 晋到 staging 报别名冲突。
		{op: "alias", now: 3665, stage: "staging", tag: "v2", digest: "v1", caller: full},
		{op: "push", now: 3666, tag: "v2", digest: "d2", caller: full},
		{op: "attest", now: 3667, digest: "d2", typ: "test", signer: "ci", exp: 10000},
		{op: "promote", now: 3727, stage: "dev", tag: "v2", caller: full, want: registry.ErrAliasConflict},
		// 反向：staging 上标签 v1 存在，别名取名 v1 报冲突。
		{op: "alias", now: 3728, stage: "staging", tag: "v1", digest: "v1", caller: full, want: registry.ErrAliasConflict},
	})
}

// TestRevokeSigner 撤销签名者后其证明失效，已完成的晋级不变。
func TestRevokeSigner(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	s := newSystem(t, specStages())
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "attest", now: 1, digest: "d1", typ: "test", signer: "ci", exp: 10000},
		{op: "attest", now: 2, digest: "d1", typ: "scan", signer: "sec", exp: 10000},
		{op: "promote", now: 60, stage: "dev", caller: full},
		{op: "promote", now: 3660, stage: "staging", caller: full},
		{op: "revoke", now: 3700, signer: "ci"},
		// 已在 release 的 v1 不变。
		{op: "resolve", stage: "release", wantDig: "d1"},
		// d2 的 test 证明来自 ci，撤销后晋级报证明缺失 test。
		{op: "push", now: 3701, digest: "d2", caller: full},
		{op: "attest", now: 3702, digest: "d2", typ: "test", signer: "ci", exp: 10000},
		{op: "promote", now: 3800, stage: "dev", caller: full, want: registry.ErrAttestation},
	})
	err := s.gate.Promote(3801, "app", "v1", "dev", full)
	if !strings.Contains(err.Error(), "test") {
		t.Fatalf("missing type should be test after revoke, got %v", err)
	}
}

// TestRejectionOrder 拒绝次序只报第一个。
func TestRejectionOrder(t *testing.T) {
	full := allPerms("dev", "staging", "release")
	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "invalid before clock",
			steps: []step{
				{op: "push", now: 10, digest: "d1", caller: full},
				{op: "promote", now: 5, stage: "nowhere", caller: full, want: registry.ErrInvalidArgument},
			},
		},
		{
			name: "clock before no-next-stage",
			steps: []step{
				{op: "push", now: 10, digest: "d1", caller: full},
				{op: "promote", now: 5, stage: "release", caller: full, want: registry.ErrClockRewind},
			},
		},
		{
			name: "no-next-stage before permission",
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "promote", now: 1, stage: "release", caller: registry.Caller{}, want: registry.ErrNoNextStage},
			},
		},
		{
			name: "permission before source-tag",
			steps: []step{
				{op: "promote", now: 0, stage: "dev", caller: registry.Caller{}, want: registry.ErrPermissionDenied},
			},
		},
		{
			name: "source-tag before dwell",
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "promote", now: 1, stage: "dev", tag: "ghost", caller: full, want: registry.ErrSourceTag},
			},
		},
		{
			name: "dwell before attestation",
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "promote", now: 30, stage: "dev", caller: full, want: registry.ErrDwell}, // 无证明但驻留先报
			},
		},
		{
			name: "attestation before alias-conflict",
			steps: []step{
				{op: "push", now: 0, tag: "v0", digest: "d0", caller: full},
				{op: "attest", now: 1, digest: "d0", typ: "test", signer: "ci", exp: 1000},
				{op: "promote", now: 60, stage: "dev", tag: "v0", caller: full},                 // staging 有 v0
				{op: "alias", now: 61, stage: "staging", tag: "v1", digest: "v0", caller: full}, // 别名 v1 占位
				{op: "push", now: 62, digest: "d1", caller: full},
				{op: "promote", now: 122, stage: "dev", caller: full, want: registry.ErrAttestation}, // 驻留够但无证明
				{op: "attest", now: 123, digest: "d1", typ: "test", signer: "ci", exp: 1000},
				{op: "promote", now: 124, stage: "dev", caller: full, want: registry.ErrAliasConflict},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newSystem(t, specStages()).run(t, tc.steps)
		})
	}

	// 墓碑先于不可变：两级仓库（末级不可变、S=0、无证明要求）。
	two := []registry.Stage{{Name: "dev"}, {Name: "rel", Immutable: true}}
	full2 := allPerms("dev", "rel")
	s := newSystem(t, two)
	s.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full2},
		{op: "promote", now: 0, stage: "dev", caller: full2},
		{op: "yank", now: 1, stage: "rel", caller: full2},
		{op: "promote", now: 2, stage: "dev", caller: full2, want: registry.ErrYanked}, // 同摘要也被墓碑拒绝
	})

	// 不可变拒绝改写：同摘要幂等、异摘要报错。
	s2 := newSystem(t, two)
	s2.run(t, []step{
		{op: "push", now: 0, digest: "d1", caller: full2},
		{op: "promote", now: 0, stage: "dev", caller: full2},
		{op: "promote", now: 1, stage: "dev", caller: full2}, // 幂等成功
		{op: "push", now: 2, digest: "d2", caller: full2},
		{op: "promote", now: 3, stage: "dev", caller: full2, want: registry.ErrImmutable},
	})
}

// model 是按题目规则写成的逐步朴素模拟，用于随机对照。
type model struct {
	stages  []registry.Stage
	trusted []map[string]bool
	tags    map[[2]string]map[string]string
	aliases map[[2]string]map[string]string
	tombs   map[[2]string]map[string]bool
	first   map[[3]string]int64
	atts    map[string][]attest.Attestation
	revoked map[string]bool
	maxNow  int64
}

func newModel(stages []registry.Stage) *model {
	m := &model{
		stages:  stages,
		tags:    map[[2]string]map[string]string{},
		aliases: map[[2]string]map[string]string{},
		tombs:   map[[2]string]map[string]bool{},
		first:   map[[3]string]int64{},
		atts:    map[string][]attest.Attestation{},
		revoked: map[string]bool{},
	}
	for _, s := range stages {
		set := map[string]bool{}
		for _, signer := range s.Trusted {
			set[signer] = true
		}
		m.trusted = append(m.trusted, set)
	}
	return m
}

func (m *model) stageIndex(name string) int {
	for i, s := range m.stages {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func (m *model) land(idx int, name, tag, digest string, now int64) error {
	st := m.stages[idx]
	key := [2]string{st.Name, name}
	if _, ok := m.aliases[key][tag]; ok {
		return registry.ErrAliasConflict
	}
	if m.tombs[key][tag] {
		return registry.ErrYanked
	}
	if cur, ok := m.tags[key][tag]; ok {
		if cur == digest {
			return nil
		}
		if st.Immutable {
			return registry.ErrImmutable
		}
	}
	if m.tags[key] == nil {
		m.tags[key] = map[string]string{}
	}
	m.tags[key][tag] = digest
	fk := [3]string{st.Name, name, digest}
	if _, ok := m.first[fk]; !ok {
		m.first[fk] = now
	}
	return nil
}

func (m *model) push(now int64, name, tag, digest string, c registry.Caller) error {
	if now < 0 || now > registry.MaxNow || name == "" || tag == "" || digest == "" {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	if !c.Has(registry.Push, m.stages[0].Name) {
		return registry.ErrPermissionDenied
	}
	if err := m.land(0, name, tag, digest, now); err != nil {
		return err
	}
	m.maxNow = now
	return nil
}

func (m *model) attestOp(now int64, digest, typ, signer string, exp int64) error {
	if now < 0 || now > registry.MaxNow || digest == "" || typ == "" || signer == "" || exp <= now {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	m.atts[digest] = append(m.atts[digest], attest.Attestation{Digest: digest, Type: typ, Signer: signer, Exp: exp})
	m.maxNow = now
	return nil
}

func (m *model) revoke(now int64, signer string) error {
	if now < 0 || now > registry.MaxNow || signer == "" {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	m.revoked[signer] = true
	m.maxNow = now
	return nil
}

func (m *model) promote(now int64, name, tag, from string, c registry.Caller) error {
	if now < 0 || now > registry.MaxNow || name == "" || tag == "" {
		return registry.ErrInvalidArgument
	}
	fi := m.stageIndex(from)
	if fi < 0 {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	if fi == len(m.stages)-1 {
		return registry.ErrNoNextStage
	}
	to := m.stages[fi+1]
	if !c.Has(registry.Promote, to.Name) {
		return registry.ErrPermissionDenied
	}
	d, ok := m.tags[[2]string{from, name}][tag]
	if !ok {
		return registry.ErrSourceTag
	}
	if now-m.first[[3]string{from, name, d}] < to.S {
		return registry.ErrDwell
	}
	valid := map[string]bool{}
	for _, a := range m.atts[d] {
		if now < a.Exp && !m.revoked[a.Signer] && m.trusted[fi+1][a.Signer] {
			valid[a.Type] = true
		}
	}
	for _, typ := range to.Required {
		if !valid[typ] {
			return fmt.Errorf("%w: %s", registry.ErrAttestation, typ)
		}
	}
	if err := m.land(fi+1, name, tag, d, now); err != nil {
		return err
	}
	m.maxNow = now
	return nil
}

func (m *model) yank(now int64, stage, name, tag string, c registry.Caller) error {
	if now < 0 || now > registry.MaxNow || name == "" || tag == "" {
		return registry.ErrInvalidArgument
	}
	idx := m.stageIndex(stage)
	if idx < 0 {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	if !c.Has(registry.Yank, stage) {
		return registry.ErrPermissionDenied
	}
	key := [2]string{stage, name}
	if _, ok := m.tags[key][tag]; !ok {
		if m.tombs[key][tag] {
			return registry.ErrYanked
		}
		return registry.ErrTagNotFound
	}
	for _, target := range m.aliases[key] {
		if target == tag {
			return registry.ErrReferenced
		}
	}
	delete(m.tags[key], tag)
	if m.stages[idx].Immutable {
		if m.tombs[key] == nil {
			m.tombs[key] = map[string]bool{}
		}
		m.tombs[key][tag] = true
	}
	m.maxNow = now
	return nil
}

func (m *model) setAlias(now int64, stage, name, alias, tag string, c registry.Caller) error {
	if now < 0 || now > registry.MaxNow || name == "" || alias == "" || tag == "" {
		return registry.ErrInvalidArgument
	}
	if m.stageIndex(stage) < 0 {
		return registry.ErrInvalidArgument
	}
	if now < m.maxNow {
		return registry.ErrClockRewind
	}
	if !c.Has(registry.Alias, stage) {
		return registry.ErrPermissionDenied
	}
	key := [2]string{stage, name}
	if _, ok := m.tags[key][tag]; !ok {
		if m.tombs[key][tag] {
			return registry.ErrYanked
		}
		return registry.ErrTagNotFound
	}
	if _, ok := m.tags[key][alias]; ok {
		return registry.ErrAliasConflict
	}
	if m.tombs[key][alias] {
		return registry.ErrAliasConflict
	}
	if m.aliases[key] == nil {
		m.aliases[key] = map[string]string{}
	}
	m.aliases[key][alias] = tag
	m.maxNow = now
	return nil
}

func (m *model) resolve(stage, name, ref string) (string, error) {
	if m.stageIndex(stage) < 0 {
		return "", registry.ErrInvalidArgument
	}
	key := [2]string{stage, name}
	if target, ok := m.aliases[key][ref]; ok {
		ref = target
	}
	if d, ok := m.tags[key][ref]; ok {
		return d, nil
	}
	if m.tombs[key][ref] {
		return "", registry.ErrYanked
	}
	return "", registry.ErrTagNotFound
}

// classOf 把错误归约为可比较的类别；证明缺失附带类型名。
func classOf(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, registry.ErrAttestation) {
		return "attestation:" + strings.TrimPrefix(err.Error(), registry.ErrAttestation.Error()+": ")
	}
	for _, s := range []struct {
		err  error
		name string
	}{
		{registry.ErrInvalidArgument, "invalid"},
		{registry.ErrClockRewind, "clock"},
		{registry.ErrNoNextStage, "nonext"},
		{registry.ErrPermissionDenied, "perm"},
		{registry.ErrSourceTag, "source"},
		{registry.ErrReferenced, "referenced"},
		{registry.ErrDwell, "dwell"},
		{registry.ErrAliasConflict, "aliasconflict"},
		{registry.ErrYanked, "yanked"},
		{registry.ErrImmutable, "immutable"},
		{registry.ErrTagNotFound, "notfound"},
	} {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	return "unknown:" + err.Error()
}

// TestRandomModelCrossCheck 用 1500 组随机操作序列对照实现与朴素模拟。
func TestRandomModelCrossCheck(t *testing.T) {
	names := []string{"app", "lib"}
	tags := []string{"v1", "v2", "v3"}
	digests := []string{"d1", "d2", "d3", "d4"}
	types := []string{"test", "test", "scan", "scan", "lint"}
	signers := []string{"ci", "ci", "sec", "sec", "ops"}
	aliases := []string{"stable", "latest", "v1", "v2"}
	stageNames := []string{"dev", "dev", "staging", "staging", "release", "nowhere"}
	pick := func(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

	seen := map[string]int{}
	for seed := int64(0); seed < 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sys := newSystem(t, specStages())
		m := newModel(specStages())
		var now int64
		check := func(i int, desc string, realErr, modelErr error) {
			t.Helper()
			rc, mc := classOf(realErr), classOf(modelErr)
			t.Logf("seed=%d op=%02d %s -> %s (model %s)", seed, i, desc, rc, mc)
			if rc != mc {
				t.Fatalf("seed=%d op=%d %s: got %s, model %s", seed, i, desc, rc, mc)
			}
			seen[strings.Split(rc, ":")[0]]++
		}
		if seed%10 == 0 {
			// 脚本化前缀：必现不可变改写拒绝，并与模型对照。
			full := allPerms("dev", "staging", "release")
			check(-6, "push(0,app,v1,d1)", sys.repo.Push(0, "app", "v1", "d1", full), m.push(0, "app", "v1", "d1", full))
			check(-5, "attest(1,d1,test,ci)", sys.store.Attest(1, "d1", "test", "ci", 100000), m.attestOp(1, "d1", "test", "ci", 100000))
			check(-4, "promote(60,app,v1,dev)", sys.gate.Promote(60, "app", "v1", "dev", full), m.promote(60, "app", "v1", "dev", full))
			check(-3, "push(61,app,v1,d2)", sys.repo.Push(61, "app", "v1", "d2", full), m.push(61, "app", "v1", "d2", full))
			check(-2, "attest(62,d2,test,ci)", sys.store.Attest(62, "d2", "test", "ci", 100000), m.attestOp(62, "d2", "test", "ci", 100000))
			check(-1, "promote(122,app,v1,dev)", sys.gate.Promote(122, "app", "v1", "dev", full), m.promote(122, "app", "v1", "dev", full))
			now = 122
		}
		for i := 0; i < 50; i++ {
			if rng.Intn(100) < 8 {
				now -= int64(rng.Intn(20)) // 时钟回退（可能变负，触发参数非法）
			} else {
				now += int64(rng.Intn(200))
			}
			caller := registry.Caller{}
			for _, a := range []registry.Action{registry.Push, registry.Promote, registry.Yank, registry.Alias} {
				for _, st := range []string{"dev", "staging", "release"} {
					if rng.Intn(100) < 75 {
						caller[registry.Permission{Action: a, Stage: st}] = true
					}
				}
			}
			name := pick(rng, names)
			tag := pick(rng, tags)
			var desc string
			var realErr, modelErr error
			switch k := rng.Intn(100); {
			case k < 25:
				d := pick(rng, digests)
				desc = fmt.Sprintf("push(%d,%s,%s,%s)", now, name, tag, d)
				realErr = sys.repo.Push(now, name, tag, d, caller)
				modelErr = m.push(now, name, tag, d, caller)
			case k < 45:
				d, typ, signer := pick(rng, digests), pick(rng, types), pick(rng, signers)
				exp := now + int64(rng.Intn(5000))
				desc = fmt.Sprintf("attest(%d,%s,%s,%s,exp=%d)", now, d, typ, signer, exp)
				realErr = sys.store.Attest(now, d, typ, signer, exp)
				modelErr = m.attestOp(now, d, typ, signer, exp)
			case k < 65:
				from := pick(rng, stageNames)
				desc = fmt.Sprintf("promote(%d,%s,%s,%s)", now, name, tag, from)
				realErr = sys.gate.Promote(now, name, tag, from, caller)
				modelErr = m.promote(now, name, tag, from, caller)
			case k < 75:
				st := pick(rng, stageNames)
				desc = fmt.Sprintf("yank(%d,%s,%s,%s)", now, st, name, tag)
				realErr = sys.repo.Yank(now, st, name, tag, caller)
				modelErr = m.yank(now, st, name, tag, caller)
				if rng.Intn(100) < 50 { // 重放同一 yank，触发墓碑/不存在分支
					check(i, desc, realErr, modelErr)
					desc = fmt.Sprintf("yank-replay(%d,%s,%s,%s)", now, st, name, tag)
					realErr = sys.repo.Yank(now, st, name, tag, caller)
					modelErr = m.yank(now, st, name, tag, caller)
				}
			case k < 85:
				st, al := pick(rng, stageNames), pick(rng, aliases)
				desc = fmt.Sprintf("alias(%d,%s,%s,%s->%s)", now, st, name, al, tag)
				realErr = sys.repo.SetAlias(now, st, name, al, tag, caller)
				modelErr = m.setAlias(now, st, name, al, tag, caller)
			case k < 90:
				signer := pick(rng, signers)
				desc = fmt.Sprintf("revoke(%d,%s)", now, signer)
				realErr = sys.store.RevokeSigner(now, signer)
				modelErr = m.revoke(now, signer)
			default:
				st, ref := pick(rng, stageNames), pick(rng, append(append([]string{}, tags...), aliases...))
				desc = fmt.Sprintf("resolve(%s,%s,%s)", st, name, ref)
				var realDig, modelDig string
				realDig, realErr = sys.repo.Resolve(st, name, ref)
				modelDig, modelErr = m.resolve(st, name, ref)
				if realDig != modelDig {
					t.Fatalf("seed=%d op=%d %s: digest %q vs model %q", seed, i, desc, realDig, modelDig)
				}
			}
			check(i, desc, realErr, modelErr)
		}
		// 序列结束后全量比对可见状态。
		for _, st := range []string{"dev", "staging", "release"} {
			for _, n := range names {
				for _, ref := range append(append([]string{}, tags...), aliases...) {
					realDig, realErr := sys.repo.Resolve(st, n, ref)
					modelDig, modelErr := m.resolve(st, n, ref)
					if realDig != modelDig || classOf(realErr) != classOf(modelErr) {
						t.Fatalf("seed=%d final resolve(%s,%s,%s): (%q,%s) vs model (%q,%s)",
							seed, st, n, ref, realDig, classOf(realErr), modelDig, classOf(modelErr))
					}
				}
			}
		}
	}
	// 确认随机序列真正覆盖到各类判定路径。
	for _, class := range []string{"ok", "invalid", "clock", "nonext", "perm", "source",
		"referenced", "dwell", "attestation", "aliasconflict", "yanked", "immutable", "notfound"} {
		if seen[class] == 0 {
			t.Fatalf("random sequences never exercised class %q", class)
		}
	}
	t.Logf("class coverage: %v", seen)
}

// TestConcurrent 并发调用等价于某个串行顺序（配合 -race 验证）。
func TestConcurrent(t *testing.T) {
	sys := newSystem(t, specStages())
	full := allPerms("dev", "staging", "release")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				now := int64(rng.Intn(5000))
				tag := fmt.Sprintf("v%d", rng.Intn(3))
				digest := fmt.Sprintf("d%d", rng.Intn(4))
				switch rng.Intn(7) {
				case 0:
					_ = sys.repo.Push(now, "app", tag, digest, full)
				case 1:
					_ = sys.store.Attest(now, digest, "test", "ci", now+1+int64(rng.Intn(10000)))
				case 2:
					_ = sys.gate.Promote(now, "app", tag, "dev", full)
				case 3:
					_ = sys.gate.Promote(now, "app", tag, "staging", full)
				case 4:
					_ = sys.repo.Yank(now, "dev", "app", tag, full)
				case 5:
					_ = sys.repo.SetAlias(now, "dev", "app", "stable", tag, full)
				case 6:
					_, _ = sys.repo.Resolve("release", "app", tag)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	// 串行收尾：系统仍可正常响应。
	if _, err := sys.repo.Resolve("dev", "app", "v1"); err != nil && !errors.Is(err, registry.ErrTagNotFound) && !errors.Is(err, registry.ErrYanked) {
		t.Fatalf("unexpected resolve error after concurrent run: %v", err)
	}
}
