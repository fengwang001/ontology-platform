package gate_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/gate"
	"ontology/owners"
	"ontology/review"
)

type op struct {
	kind   string
	id     int
	user   string
	user2  string
	files  []string
	draft  bool
	v      review.Verdict
	check  string
	head   int
	status review.Status
	level  review.Level
}

func (o op) String() string {
	return fmt.Sprintf("{kind=%s id=%d user=%q user2=%q files=%v draft=%v v=%d check=%q head=%d status=%d level=%d}",
		o.kind, o.id, o.user, o.user2, o.files, o.draft, o.v, o.check, o.head, o.status, o.level)
}

func opsLog(log []op) string {
	var b strings.Builder
	for i, o := range log {
		fmt.Fprintf(&b, "  [%d] %s\n", i, o)
	}
	return b.String()
}

func pickFiles(rng *rand.Rand, pool []string) []string {
	n := 1 + rng.Intn(4)
	if n > len(pool) {
		n = len(pool)
	}
	idx := rng.Perm(len(pool))[:n]
	files := make([]string, n)
	for i, j := range idx {
		files[i] = pool[j]
	}
	sort.Strings(files)
	return files
}

// apply 对真实 Gate 与模拟器执行同一操作，返回两边的错误。
func (o op) apply(g *gate.Gate, s *simulator) (got, want error) {
	switch o.kind {
	case "setperm":
		return g.SetPerm(o.user, o.level), s.setPerm(o.user, o.level)
	case "open":
		// open 的编号在外面比对
		_, got = g.Open(o.user, o.files, o.draft)
		_, want = s.open(o.user, o.files, o.draft)
		return
	case "push":
		return g.Push(o.id, o.files), s.push(o.id, o.files)
	case "ready":
		return g.Ready(o.id), s.ready(o.id)
	case "update":
		return g.UpdateBranch(o.id), s.updateBranch(o.id)
	case "review":
		return g.Review(o.id, o.user, o.v), s.review(o.id, o.user, o.v)
	case "dismiss":
		return g.Dismiss(o.id, o.user2, o.user), s.dismiss(o.id, o.user2, o.user)
	case "report":
		return g.Report(o.id, o.check, o.head, o.status), s.report(o.id, o.check, o.head, o.status)
	case "merge":
		return g.Merge(o.id, o.user), s.merge(o.id, o.user)
	case "mergeable":
		got = g.Mergeable(o.id)
		want, _ = s.mergeable(o.id)
		return
	}
	panic("unknown kind " + o.kind)
}

// snapshotState 对比两边可观察状态：T、每个 PR 的 base/head/merged/draft/files，
// 以及关键用户裁决与检查存档。
func assertStatesEqual(t *testing.T, seq int, log []op, g *gate.Gate, s *simulator) {
	t.Helper()
	if g.T() != s.t {
		t.Fatalf("seq=%d T mismatch: gate=%d sim=%d\n%s", seq, g.T(), s.t, opsLog(log))
	}
	ids := make([]int, 0, len(s.prs))
	for id := range s.prs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		sp := s.prs[id]
		base, head, merged, draft, ok := g.BaseHead(id)
		if !ok {
			t.Fatalf("seq=%d pr %d missing in gate\n%s", seq, id, opsLog(log))
		}
		if base != sp.base || head != sp.head || merged != sp.merged || draft != sp.draft {
			t.Fatalf("seq=%d pr %d mismatch gate=(b=%d,h=%d,m=%v,d=%v) sim=(b=%d,h=%d,m=%v,d=%v)\n%s",
				seq, id, base, head, merged, draft, sp.base, sp.head, sp.merged, sp.draft, opsLog(log))
		}
		gfiles, _ := g.Files(id)
		if strings.Join(gfiles, ",") != strings.Join(sp.files, ",") {
			t.Fatalf("seq=%d pr %d files mismatch gate=%v sim=%v\n%s", seq, id, gfiles, sp.files, opsLog(log))
		}
		for k, sv := range s.verdict {
			if k.pr != id {
				continue
			}
			gv, ok := g.Verdict(id, k.user)
			if !ok || gv != sv {
				t.Fatalf("seq=%d verdict (%d,%s) gate=(%v,%v) sim=%v\n%s",
					seq, id, k.user, gv, ok, sv, opsLog(log))
			}
		}
		for k, ss := range s.checks {
			if k.pr != id {
				continue
			}
			gs, ok := g.Check(id, k.check, k.head)
			if !ok || gs != ss {
				t.Fatalf("seq=%d check (%d,%s@%d) gate=(%v,%v) sim=%v\n%s",
					seq, id, k.check, k.head, gs, ok, ss, opsLog(log))
			}
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	const sequences = 1500
	const maxOps = 80

	users := []string{"bob", "carol", "dave", "erin", "alice", "frank"}
	filePool := []string{
		"main.go", "util.go", "api/x.go", "api/y.go",
		"docs/a.md", "docs/b.md", "docs/gen.go",
		"README", "Makefile", "src/lib/core.go",
	}
	checks := []string{"build", "test", "lint"}
	verdicts := []review.Verdict{review.Approve, review.RequestChanges, review.Comment}
	statuses := []review.Status{review.Pending, review.Success, review.Failure, review.Neutral, review.Skipped}
	levels := []review.Level{review.None, review.Write, review.Admin}
	rules := []owners.Rule{
		{Pattern: "*", Owners: []string{"alice"}},
		{Pattern: "docs/", Owners: nil},
		{Pattern: "*.go", Owners: []string{"bob", "carol"}},
		{Pattern: "api/", Owners: []string{"dave"}},
	}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 17))
		cfg := gate.Config{
			N:             rng.Intn(3),
			DismissStale:  rng.Intn(2) == 0,
			RequireOwners: rng.Intn(2) == 0,
			Strict:        rng.Intn(2) == 0,
			Required:      append([]string(nil), checks[:rng.Intn(len(checks)+1)]...),
		}
		g := gate.New(cfg, rules)
		sim := newSimulator(cfg, rules)

		var log []op
		for step := 0; step < maxOps; step++ {
			existing := len(sim.prs)
			o := op{kind: "noop"}

			pickID := func() int {
				if existing == 0 {
					return 1 + rng.Intn(3) // 大概率不存在
				}
				if rng.Intn(5) == 0 {
					return existing + 1 + rng.Intn(3)
				}
				return 1 + rng.Intn(existing)
			}

			switch rng.Intn(11) {
			case 0:
				o.kind = "setperm"
				o.user = users[rng.Intn(len(users))]
				o.level = levels[rng.Intn(len(levels))]
			case 1:
				o.kind = "open"
				o.user = users[rng.Intn(len(users))]
				o.files = pickFiles(rng, filePool)
				o.draft = rng.Intn(2) == 0
			case 2:
				o.kind = "push"
				o.id = pickID()
				o.files = pickFiles(rng, filePool)
			case 3:
				o.kind = "ready"
				o.id = pickID()
			case 4:
				o.kind = "update"
				o.id = pickID()
			case 5:
				o.kind = "review"
				o.id = pickID()
				o.user = users[rng.Intn(len(users))]
				o.v = verdicts[rng.Intn(len(verdicts))]
			case 6:
				o.kind = "dismiss"
				o.id = pickID()
				o.user2 = users[rng.Intn(len(users))]
				o.user = users[rng.Intn(len(users))] // actor
			case 7:
				o.kind = "report"
				o.id = pickID()
				o.check = checks[rng.Intn(len(checks))]
				o.head = 1 + rng.Intn(4) // 有时超前
				o.status = statuses[rng.Intn(len(statuses))]
			case 8:
				o.kind = "merge"
				o.id = pickID()
				o.user = users[rng.Intn(len(users))]
			case 9:
				o.kind = "mergeable"
				o.id = pickID()
			case 10:
				// 初始先保证几个用户有非 none 权限，增加合并路径覆盖。
				o.kind = "setperm"
				o.user = users[rng.Intn(4)]
				o.level = review.Write
			}

			got, want := o.apply(g, sim)
			log = append(log, o)
			if reasonKey(got) != reasonKey(want) {
				t.Fatalf("seq=%d step=%d cfg=%+v\nerror mismatch on %s\n got=%v\nwant=%v\n%s",
					seq, step, cfg, o, got, want, opsLog(log))
			}
			if o.kind == "open" && got == nil {
				assertStatesEqual(t, seq, log, g, sim)
			}
		}
		// 对每个存在的 PR 比较最终 Mergeable 判定类别与明细。
		for id := range sim.prs {
			gerr := g.Mergeable(id)
			serr, detail := sim.mergeable(id)
			if reasonKey(gerr) != reasonKey(serr) {
				t.Fatalf("seq=%d final mergeable pr=%d gate=%v sim=%v\n%s",
					seq, id, gerr, serr, opsLog(log))
			}
			if detail != "" && (gerr == nil || !strings.Contains(gerr.Error(), detail)) {
				t.Fatalf("seq=%d pr=%d detail mismatch gate=%v want-file=%s\n%s",
					seq, id, gerr, detail, opsLog(log))
			}
		}
		assertStatesEqual(t, seq, log, g, sim)
		if seq < 3 {
			t.Logf("seq=%d cfg=%+v replayed %d ops OK; final T gate=%d sim=%d",
				seq, cfg, len(log), g.T(), sim.t)
		}
	}

	// 日志中打印输入、输出与判定依据：抽验若干序列的完整轨迹。
	t.Run("logged-sample", func(t *testing.T) {
		rng := rand.New(rand.NewSource(42))
		cfg := gate.Config{N: 1, DismissStale: true, RequireOwners: true, Strict: true, Required: checks}
		g := gate.New(cfg, rules)
		sim := newSimulator(cfg, rules)
		var log []op
		for step := 0; step < 25; step++ {
			o := op{kind: "noop"}
			existing := len(sim.prs)
			pickID := func() int {
				if existing == 0 {
					return 1
				}
				return 1 + rng.Intn(existing+1)
			}
			switch rng.Intn(9) {
			case 0:
				o.kind, o.user, o.level = "setperm", users[rng.Intn(len(users))], levels[rng.Intn(len(levels))]
			case 1:
				o.kind, o.user, o.files, o.draft = "open", users[rng.Intn(len(users))], pickFiles(rng, filePool), rng.Intn(2) == 0
			case 2:
				o.kind, o.id, o.files = "push", pickID(), pickFiles(rng, filePool)
			case 3:
				o.kind, o.id = "ready", pickID()
			case 4:
				o.kind, o.id = "update", pickID()
			case 5:
				o.kind, o.id, o.user, o.v = "review", pickID(), users[rng.Intn(len(users))], verdicts[rng.Intn(len(verdicts))]
			case 6:
				o.kind, o.id, o.check, o.head, o.status = "report", pickID(), checks[rng.Intn(len(checks))], 1+rng.Intn(3), statuses[rng.Intn(len(statuses))]
			case 7:
				o.kind, o.id, o.user = "merge", pickID(), users[rng.Intn(len(users))]
			case 8:
				o.kind, o.id = "mergeable", pickID()
			}
			got, want := o.apply(g, sim)
			log = append(log, o)
			basis := "-"
			if o.kind == "mergeable" {
				if e, detail := sim.mergeable(o.id); e != nil {
					basis = reasonKey(e)
					if detail != "" {
						basis += ":" + detail
					}
				} else {
					basis = "<mergeable>"
				}
			}
			t.Logf("step=%2d INPUT %s | OUTPUT gate=%v sim=%v | BASIS %s", step, o, got, want, basis)
			if reasonKey(got) != reasonKey(want) {
				t.Fatalf("mismatch: gate=%v sim=%v", got, want)
			}
		}
	})
}
