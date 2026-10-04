package promote_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/promote"
	"ontology/registry"
)

type simTag struct {
	digest string
	tomb   bool
}

type simAtt struct {
	typ    string
	signer string
	exp    int64
}

// naiveSim 用独立的直白状态镜像逐步实现同一套规则，不调用被测实现。
type naiveSim struct {
	stages  []registry.Stage
	tags    map[string]map[string]map[string]simTag
	aliases map[string]map[string]map[string]string
	first   map[string]map[string]int64
	atts    map[string][]simAtt
	revoked map[string]bool
	nowMax  int64
}

func newNaive(stages []registry.Stage) *naiveSim {
	s := &naiveSim{
		stages:  stages,
		tags:    map[string]map[string]map[string]simTag{},
		aliases: map[string]map[string]map[string]string{},
		first:   map[string]map[string]int64{},
		atts:    map[string][]simAtt{},
		revoked: map[string]bool{},
	}
	for _, st := range stages {
		s.tags[st.Name] = map[string]map[string]simTag{}
		s.aliases[st.Name] = map[string]map[string]string{}
		s.first[st.Name] = map[string]int64{}
	}
	return s
}

func (s *naiveSim) stageIdx(name string) int {
	for i, st := range s.stages {
		if st.Name == name {
			return i
		}
	}
	return -1
}

func (s *naiveSim) place(stageIdx int, name, tag, digest string) error {
	st := s.stages[stageIdx]
	if _, ok := s.aliases[st.Name][name][tag]; ok {
		return promote.ErrAliasClash
	}
	bucket := s.tags[st.Name][name]
	if bucket == nil {
		bucket = map[string]simTag{}
		s.tags[st.Name][name] = bucket
	}
	cur, exists := bucket[tag]
	if exists && cur.tomb {
		return promote.ErrTagYanked
	}
	if exists && st.Immutable && cur.digest != digest {
		return promote.ErrImmutable
	}
	bucket[tag] = simTag{digest: digest}
	return nil
}

func firstKey(name, digest string) string { return name + "\x00" + digest }

func (s *naiveSim) setFirst(stage, name, digest string, now int64) {
	bm := s.first[stage]
	k := firstKey(name, digest)
	if _, ok := bm[k]; !ok {
		bm[k] = now
	}
}

func (s *naiveSim) getFirst(stage, name, digest string) (int64, bool) {
	v, ok := s.first[stage][firstKey(name, digest)]
	return v, ok
}

func errClass(err error) string {
	switch {
	case errors.Is(err, promote.ErrAliasClash):
		return "aliasclash"
	case errors.Is(err, promote.ErrTagYanked):
		return "yanked"
	case errors.Is(err, promote.ErrImmutable):
		return "immutable"
	case errors.Is(err, promote.ErrNotFound):
		return "notfound"
	}
	return "?"
}

type simOp struct {
	kind                           string
	now                            int64
	name, tag, digest, typ, signer string
	stage, from, alias             string
	exp                            int64
}

func (op simOp) valid(s *naiveSim) bool {
	if op.now < 0 || op.now > 1_000_000_000_000 {
		return false
	}
	switch op.kind {
	case "push":
		return op.name != "" && op.tag != "" && op.digest != ""
	case "attest", "revoke":
		return op.signer != "" && (op.kind == "revoke" || op.digest != "" && op.typ != "")
	case "promote":
		return op.name != "" && op.tag != "" && s.stageIdx(op.from) >= 0
	case "yank":
		return op.name != "" && op.tag != "" && s.stageIdx(op.stage) >= 0
	case "alias":
		return op.name != "" && op.tag != "" && op.alias != "" && s.stageIdx(op.stage) >= 0
	}
	return false
}

func (s *naiveSim) run(op simOp, perms map[registry.Permission]bool) string {
	if !op.valid(s) {
		return "invalid"
	}
	if op.now < s.nowMax {
		return "clock"
	}
	allowed := func(act registry.Action, stage string) bool {
		return perms[registry.Permission{Action: act, Stage: stage}]
	}
	switch op.kind {
	case "push":
		if !allowed(registry.Push, s.stages[0].Name) {
			return "forbidden"
		}
		if err := s.place(0, op.name, op.tag, op.digest); err != nil {
			return errClass(err)
		}
		s.setFirst(s.stages[0].Name, op.name, op.digest, op.now)
	case "attest":
		if op.exp <= op.now {
			return "invalid"
		}
		s.atts[op.digest] = append(s.atts[op.digest], simAtt{op.typ, op.signer, op.exp})
	case "revoke":
		s.revoked[op.signer] = true
	case "promote":
		fi := s.stageIdx(op.from)
		if fi >= len(s.stages)-1 {
			return "nonext"
		}
		to := s.stages[fi+1]
		if !allowed(registry.Promote, to.Name) {
			return "forbidden"
		}
		cur, ok := s.tags[op.from][op.name][op.tag]
		if !ok || cur.tomb {
			return "source"
		}
		first, ok := s.getFirst(op.from, op.name, cur.digest)
		if !ok {
			return "source"
		}
		if op.now-first < to.S {
			return "dwell"
		}
		have := map[string]bool{}
		for _, a := range s.atts[cur.digest] {
			if op.now < a.exp && !s.revoked[a.signer] && to.Trusted[a.signer] {
				have[a.typ] = true
			}
		}
		for _, req := range to.Required {
			if !have[req] {
				return "proof:" + req
			}
		}
		if err := s.place(fi+1, op.name, op.tag, cur.digest); err != nil {
			return errClass(err)
		}
		s.setFirst(to.Name, op.name, cur.digest, op.now)
	case "yank":
		fi := s.stageIdx(op.stage)
		if !allowed(registry.Yank, op.stage) {
			return "forbidden"
		}
		cur, ok := s.tags[op.stage][op.name][op.tag]
		if !ok {
			return "notfound"
		}
		if cur.tomb {
			return "yanked"
		}
		for _, m := range s.aliases[op.stage][op.name] {
			if m == op.tag {
				return "referenced"
			}
		}
		if s.stages[fi].Immutable {
			s.tags[op.stage][op.name][op.tag] = simTag{digest: cur.digest, tomb: true}
		} else {
			delete(s.tags[op.stage][op.name], op.tag)
		}
	case "alias":
		if !allowed(registry.Alias, op.stage) {
			return "forbidden"
		}
		cur, ok := s.tags[op.stage][op.name][op.tag]
		if !ok {
			return "notfound"
		}
		if cur.tomb {
			return "yanked"
		}
		am := s.aliases[op.stage][op.name]
		if am == nil {
			am = map[string]string{}
			s.aliases[op.stage][op.name] = am
		}
		if _, exists := am[op.alias]; !exists {
			if _, clash := s.tags[op.stage][op.name][op.alias]; clash {
				return "aliasclash"
			}
		}
		am[op.alias] = op.tag
	}
	s.nowMax = op.now
	return "ok"
}

func (op simOp) apply(repo *promote.Repo, c promote.Caller) error {
	switch op.kind {
	case "push":
		return repo.Push(op.now, op.name, op.tag, op.digest, c)
	case "attest":
		return repo.Attest(op.now, op.digest, op.typ, op.signer, op.exp)
	case "revoke":
		return repo.RevokeSigner(op.now, op.signer)
	case "promote":
		return repo.Promote(op.now, op.name, op.tag, op.from, c)
	case "yank":
		return repo.Yank(op.now, op.stage, op.name, op.tag, c)
	case "alias":
		return repo.SetAlias(op.now, op.stage, op.name, op.alias, op.tag, c)
	}
	return nil
}

func realClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, promote.ErrInvalidParam):
		return "invalid"
	case errors.Is(err, promote.ErrClockRollback):
		return "clock"
	case errors.Is(err, promote.ErrNoNextStage):
		return "nonext"
	case errors.Is(err, promote.ErrForbidden):
		return "forbidden"
	case errors.Is(err, promote.ErrSource):
		return "source"
	case errors.Is(err, promote.ErrDwell):
		return "dwell"
	case errors.Is(err, promote.ErrProofMissing):
		return "proof:" + strings.TrimPrefix(err.Error(), "missing proof: ")
	case errors.Is(err, promote.ErrReferenced):
		return "referenced"
	case errors.Is(err, promote.ErrAliasClash):
		return "aliasclash"
	case errors.Is(err, promote.ErrTagYanked):
		return "yanked"
	case errors.Is(err, promote.ErrImmutable):
		return "immutable"
	case errors.Is(err, promote.ErrNotFound):
		return "notfound"
	}
	return "?"
}

func TestRandomizedAgainstNaive(t *testing.T) {
	const sequences, length = 1500, 30
	stages := threeStages()
	stageNames := []string{"dev", "staging", "release"}
	names := []string{"app", "svc"}
	tags := []string{"v1", "v2", "v3"}
	digests := []string{"d1", "d2", "d3", "d4"}
	atypes := []string{"test", "scan"}
	signers := []string{"ci", "sec", "robot"}
	kinds := []string{"push", "attest", "revoke", "promote", "yank", "alias"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		repo, err := promote.New(stages)
		if err != nil {
			t.Fatal(err)
		}
		sim := newNaive(stages)

		perms := map[registry.Permission]bool{}
		var plist []registry.Permission
		acts := []registry.Action{registry.Push, registry.Promote, registry.Yank, registry.Alias}
		for _, a := range acts {
			for _, sn := range stageNames {
				if rng.Intn(2) == 0 {
					p := registry.Permission{Action: a, Stage: sn}
					perms[p] = true
					plist = append(plist, p)
				}
			}
		}
		c := promote.Caller{Permissions: plist}

		var log []string
		now := int64(0)
		for i := 0; i < length; i++ {
			kind := kinds[rng.Intn(len(kinds))]
			op := simOp{
				kind: kind,
				name: names[rng.Intn(len(names))],
				tag:  tags[rng.Intn(len(tags))],
			}
			if rng.Intn(6) == 0 && now >= 2 {
				op.now = now - rng.Int63n(3) - 1 // 时钟回退
			} else {
				now += rng.Int63n(120)
				op.now = now
			}
			switch kind {
			case "push":
				op.digest = digests[rng.Intn(len(digests))]
			case "attest":
				op.digest = digests[rng.Intn(len(digests))]
				op.typ = atypes[rng.Intn(len(atypes))]
				op.signer = signers[rng.Intn(len(signers))]
				op.exp = op.now + 1 + rng.Int63n(5000)
			case "revoke":
				op.signer = signers[rng.Intn(len(signers))]
			case "promote":
				op.from = stageNames[rng.Intn(3)]
			case "yank":
				op.stage = stageNames[rng.Intn(3)]
			case "alias":
				op.stage = stageNames[rng.Intn(3)]
				op.alias = []string{"stable", "v2", "v9"}[rng.Intn(3)]
			}
			if rng.Intn(20) == 0 {
				op.now = -1 // 参数非法
			}

			realErr := op.apply(repo, c)
			got := realClass(realErr)
			wantC := sim.run(op, perms)
			entry := fmt.Sprintf("seq=%d step=%d op=%+v => real=%q naive=%q err=%v",
				seq, i, op, got, wantC, realErr)
			log = append(log, entry)
			if got != wantC {
				t.Fatalf("mismatch\n%s\n--- trace ---\n%s", entry, strings.Join(log, "\n"))
			}
		}
		if testing.Verbose() && seq < 3 {
			t.Logf("seq %d:\n%s", seq, strings.Join(log, "\n"))
		}
	}
}

func TestScannedIndependentOfDigestCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		r, _ := promote.New(threeStages())
		c := full()
		if err := r.Push(0, "a", "t", "d0", c); err != nil {
			t.Fatal(err)
		}
		// 给 n-1 个无关摘要各塞若干证明，验证检视条数与库内摘要总数无关。
		for i := 1; i < n; i++ {
			d := fmt.Sprintf("dx%d", i)
			if err := r.Attest(0, d, "test", "ci", 100000); err != nil {
				t.Fatal(err)
			}
			if err := r.Attest(0, d, "scan", "sec", 100000); err != nil {
				t.Fatal(err)
			}
		}
		// d0 自己 3 条证明（含 1 条无效签名者）。
		if err := r.Attest(0, "d0", "test", "ci", 100000); err != nil {
			t.Fatal(err)
		}
		if err := r.Attest(0, "d0", "scan", "sec", 100000); err != nil {
			t.Fatal(err)
		}
		if err := r.Attest(0, "d0", "test", "robot", 100000); err != nil {
			t.Fatal(err)
		}
		if err := r.Promote(60, "a", "t", "dev", c); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if scanned := r.AttScanned(); scanned != 3 {
			t.Fatalf("n=%d scanned=%d want 3 (only d0's own proofs)", n, scanned)
		}
	}
}

func TestResolveTouchesAtMostTwo(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(0, "a", "v1", "d1", c)
	_ = r.SetAlias(0, "dev", "a", "stable", "v1", c)
	if _, err := r.Resolve("dev", "a", "v1"); err != nil {
		t.Fatal(err)
	}
	if n := r.ResolveTouched(); n > 2 {
		t.Fatalf("direct resolve touched=%d", n)
	}
	if d, err := r.Resolve("dev", "a", "stable"); err != nil || d != "d1" {
		t.Fatalf("alias resolve: %q %v", d, err)
	}
	if n := r.ResolveTouched(); n > 2 {
		t.Fatalf("alias resolve touched=%d", n)
	}
}
