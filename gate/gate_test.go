package gate

import (
	"errors"
	"ontology/owners"
	"ontology/review"
	"strings"
	"sync"
	"testing"
)

func exampleGate() *Gate {
	cfg := Config{N: 2, DismissStale: true, RequireOwners: true, Strict: true, Required: []string{"build", "test"}}
	rules := []owners.Rule{
		{Pattern: "*", Owners: []string{"alice"}},
		{Pattern: "docs/", Owners: nil},
		{Pattern: "*.go", Owners: []string{"bob", "carol"}},
		{Pattern: "api/", Owners: []string{"dave"}},
	}
	g := New(cfg, rules)
	for _, u := range []string{"bob", "carol", "dave", "erin"} {
		if err := g.SetPerm(u, review.Write); err != nil {
			panic(err)
		}
	}
	return g
}

func approveT(t *testing.T, g *Gate, id int, user string) {
	t.Helper()
	if err := g.Review(id, user, review.Approve); err != nil {
		t.Fatalf("approve by %s: %v", user, err)
	}
}

func reportT(t *testing.T, g *Gate, id int, check string, head int, st review.Status) {
	t.Helper()
	if err := g.Report(id, check, head, st); err != nil {
		t.Fatalf("report %s@%d: %v", check, head, err)
	}
}

func passChecks(t *testing.T, g *Gate, id, head int) {
	t.Helper()
	reportT(t, g, id, "build", head, review.Success)
	reportT(t, g, id, "test", head, review.Success)
}

// TestCanonicalScenario 复现题目给出的完整续例。
func TestCanonicalScenario(t *testing.T) {
	g := exampleGate()
	id, err := g.Open("bob", []string{"main.go", "api/x.go", "docs/a.md"}, false)
	if err != nil || id != 1 {
		t.Fatalf("Open = %d,%v", id, err)
	}

	approveT(t, g, id, "carol")
	approveT(t, g, id, "dave")
	if err := g.Mergeable(id); !errors.Is(err, ErrCheckPending) {
		t.Fatalf("approval+owner terms satisfied; want check pending, got %v", err)
	}

	if err := g.Review(id, "erin", review.RequestChanges); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(g.Mergeable(id), ErrChangeRequest) {
		t.Fatalf("want change request, got %v", g.Mergeable(id))
	}
	if err := g.Review(id, "erin", review.Comment); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(g.Mergeable(id), ErrChangeRequest) {
		t.Fatalf("comment must not clear verdict, got %v", g.Mergeable(id))
	}
	if err := g.SetPerm("erin", review.None); err != nil {
		t.Fatal(err)
	}
	if err := g.Mergeable(id); err != nil && !errors.Is(err, ErrCheckPending) {
		t.Fatalf("demoted erin: want checks pending, got %v", err)
	}
	if err := g.SetPerm("erin", review.Write); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(g.Mergeable(id), ErrChangeRequest) {
		t.Fatalf("restored erin should block again, got %v", g.Mergeable(id))
	}

	approveT(t, g, id, "erin")
	reportT(t, g, id, "build", 1, review.Pending)
	reportT(t, g, id, "test", 1, review.Failure)
	if err := g.Mergeable(id); !errors.Is(err, ErrCheckFailure) || !strings.Contains(err.Error(), "test") {
		t.Fatalf("failure test reported first, got %v", err)
	}
	passChecks(t, g, id, 1)
	if err := g.Mergeable(id); err != nil {
		t.Fatalf("ready to merge, got %v", err)
	}

	other, err := g.Open("bob", []string{"docs/other.md"}, false)
	if err != nil {
		t.Fatal(err)
	}
	approveT(t, g, other, "carol")
	approveT(t, g, other, "dave")
	passChecks(t, g, other, 1)
	if err := g.Merge(other, "bob"); err != nil {
		t.Fatal(err)
	}
	if g.T() != 1 {
		t.Fatalf("T = %d, want 1", g.T())
	}
	if !errors.Is(g.Mergeable(id), ErrStaleBase) {
		t.Fatalf("want stale base, got %v", g.Mergeable(id))
	}

	if err := g.UpdateBranch(id); err != nil {
		t.Fatal(err)
	}
	base, head, _, _, _ := g.BaseHead(id)
	if base != 1 || head != 2 {
		t.Fatalf("base,head = %d,%d", base, head)
	}
	for _, u := range []string{"carol", "dave", "erin"} {
		if v, ok := g.Verdict(id, u); !ok || v != review.Approve {
			t.Fatalf("approval of %s lost after UpdateBranch", u)
		}
	}
	if err := g.Mergeable(id); !errors.Is(err, ErrCheckPending) || !strings.Contains(err.Error(), "build") {
		t.Fatalf("checks need rerun at head 2, got %v", err)
	}
	reportT(t, g, id, "build", 1, review.Success)
	reportT(t, g, id, "test", 1, review.Success)
	if !errors.Is(g.Mergeable(id), ErrCheckPending) {
		t.Fatalf("late stale-head reports ignored, got %v", g.Mergeable(id))
	}
	reportT(t, g, id, "build", 2, review.Neutral)
	reportT(t, g, id, "test", 2, review.Skipped)
	if err := g.Mergeable(id); err != nil {
		t.Fatalf("neutral/skipped pass, got %v", err)
	}

	if err := g.Review(id, "erin", review.RequestChanges); err != nil {
		t.Fatal(err)
	}
	before := g.Touched()
	if err := g.Push(id, []string{"main.go"}); err != nil {
		t.Fatal(err)
	}
	if _, head, _, _, _ := g.BaseHead(id); head != 3 {
		t.Fatalf("head = %d, want 3", head)
	}
	if v, ok := g.Verdict(id, "erin"); !ok || v != review.RequestChanges {
		t.Fatalf("request-changes must survive push, got %v,%v", v, ok)
	}
	if _, ok := g.Verdict(id, "carol"); ok {
		t.Fatal("approvals must be dismissed on author push")
	}
	if !errors.Is(g.Mergeable(id), ErrChangeRequest) {
		t.Fatalf("stale request-changes blocks first, got %v", g.Mergeable(id))
	}
	if g.Touched() != before {
		t.Fatal("push must not touch Review/Report records count")
	}
}

// TestOwnerSelfExempt 作者是唯一属主（或文件无属主）时免除属主批准项。
func TestOwnerSelfExempt(t *testing.T) {
	g := exampleGate()
	id, _ := g.Open("alice", []string{"README", "docs/a.md"}, false)
	approveT(t, g, id, "bob")
	approveT(t, g, id, "erin")
	passChecks(t, g, id, 1)
	if err := g.Mergeable(id); err != nil {
		t.Fatalf("self-only owner exempt, got %v", err)
	}
}

// TestMissingOwnerReportsLexMinFile 报字节序最小的缺失文件。
func TestMissingOwnerReportsLexMinFile(t *testing.T) {
	g := New(Config{N: 0, RequireOwners: true}, []owners.Rule{{Pattern: "*", Owners: []string{"alice"}}})
	id, _ := g.Open("bob", []string{"z.go", "a.go", "m.go"}, false)
	err := g.Mergeable(id)
	if !errors.Is(err, ErrOwnerApproval) || !strings.HasSuffix(err.Error(), "a.go") {
		t.Fatalf("want owner approval for a.go, got %v", err)
	}
}

func reasonIs(err, target error) bool { return errors.Is(err, target) }

// TestMergeableReasonOrder 表驱动验证八个拦截原因的固定次序。
func TestMergeableReasonOrder(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, g *Gate) int
		want   error
		detail string
	}{
		{
			name: "merged first",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"docs/a.md"}, true)
				g.Ready(id)
				approveT(t, g, id, "carol")
				approveT(t, g, id, "dave")
				passChecks(t, g, id, 1)
				if err := g.Merge(id, "bob"); err != nil {
					t.Fatal(err)
				}
				return id
			},
			want: ErrMerged,
		},
		{
			name: "draft before change request",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"main.go"}, true)
				if err := g.Review(id, "erin", review.RequestChanges); err != nil {
					t.Fatal(err)
				}
				return id
			},
			want: ErrDraft,
		},
		{
			name: "change request before approval count",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"main.go"}, false)
				if err := g.Review(id, "erin", review.RequestChanges); err != nil {
					t.Fatal(err)
				}
				return id
			},
			want: ErrChangeRequest,
		},
		{
			name: "approval count before owners",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"main.go"}, false)
				approveT(t, g, id, "carol")
				return id
			},
			want: ErrApprovals,
		},
		{
			name: "owners before checks",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"main.go"}, false)
				approveT(t, g, id, "bobx")
				approveT(t, g, id, "erin")
				return id
			},
			want:   ErrOwnerApproval,
			detail: "main.go",
		},
		{
			name: "failure before pending and list order",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"docs/a.md"}, false)
				approveT(t, g, id, "bobx")
				approveT(t, g, id, "erin")
				reportT(t, g, id, "build", 1, review.Pending)
				reportT(t, g, id, "test", 1, review.Failure)
				return id
			},
			want:   ErrCheckFailure,
			detail: "test",
		},
		{
			name: "pending before stale base",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"docs/a.md"}, false)
				approveT(t, g, id, "bobx")
				approveT(t, g, id, "erin")
				other, _ := g.Open("bob", []string{"docs/b.md"}, false)
				approveT(t, g, other, "carol")
				approveT(t, g, other, "dave")
				passChecks(t, g, other, 1)
				if err := g.Merge(other, "bob"); err != nil {
					t.Fatal(err)
				}
				return id
			},
			want:   ErrCheckPending,
			detail: "build",
		},
		{
			name: "stale base last",
			setup: func(t *testing.T, g *Gate) int {
				id, _ := g.Open("bob", []string{"docs/a.md"}, false)
				approveT(t, g, id, "bobx")
				approveT(t, g, id, "erin")
				passChecks(t, g, id, 1)
				other, _ := g.Open("bob", []string{"docs/b.md"}, false)
				approveT(t, g, other, "carol")
				approveT(t, g, other, "dave")
				passChecks(t, g, other, 1)
				if err := g.Merge(other, "bob"); err != nil {
					t.Fatal(err)
				}
				return id
			},
			want: ErrStaleBase,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := exampleGate()
			if err := g.SetPerm("bobx", review.Write); err != nil {
				t.Fatal(err)
			}
			id := tc.setup(t, g)
			err := g.Mergeable(id)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Mergeable = %v, want %v", err, tc.want)
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("err %q missing detail %q", err.Error(), tc.detail)
			}
		})
	}
}

// TestRejectionOrder 验证拒绝次序：参数非法 > 不存在 > 无权限 > 状态不符 > 自评。
func TestRejectionOrder(t *testing.T) {
	g := exampleGate()
	if err := g.SetPerm("admin", review.Admin); err != nil {
		t.Fatal(err)
	}
	id, _ := g.Open("bob", []string{"main.go"}, false)

	if err := g.Push(999, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Push bad files = %v", err)
	}
	if err := g.Push(id, []string{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Push empty files = %v", err)
	}
	if err := g.Push(id, []string{"a", "a"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Push dup files = %v", err)
	}
	if _, err := g.Open("", []string{"a"}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Open empty author = %v", err)
	}
	if err := g.Review(id, "x", review.Verdict(9)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict = %v", err)
	}
	if err := g.Report(id, "c", 0, review.Success); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad head = %v", err)
	}
	if err := g.Report(id, "c", 1, review.Status(9)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status = %v", err)
	}

	if err := g.Ready(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Ready missing = %v", err)
	}
	if err := g.Mergeable(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Mergeable missing = %v", err)
	}
	if err := g.Dismiss(999, "carol", "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Dismiss missing pr = %v, want not found", err)
	}
	// PR 存在但 actor 非 admin：无权限先于状态。
	if err := g.Dismiss(id, "carol", "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Dismiss by writer = %v, want forbidden", err)
	}

	// 无权限先于状态：none 用户合并已合并 PR 仍报无权限。
	merged, _ := g.Open("bob", []string{"docs/c.md"}, true)
	g.Ready(merged)
	approveT(t, g, merged, "carol")
	approveT(t, g, merged, "dave")
	passChecks(t, g, merged, 1)
	if err := g.Merge(merged, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := g.Merge(merged, "noperm"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("merged + no perm = %v", err)
	}
	// 状态不符：已合并 PR 的各种变更操作。
	for _, err := range []error{
		g.Push(merged, []string{"docs/d.md"}),
		g.Ready(merged),
		g.UpdateBranch(merged),
		g.Review(merged, "carol", review.Approve),
		g.Report(merged, "build", 1, review.Success),
		g.Dismiss(merged, "carol", "admin"),
	} {
		if !errors.Is(err, ErrState) {
			t.Fatalf("op on merged = %v, want state", err)
		}
	}

	// 状态先于自评：已合并 PR 作者自评报状态。
	if err := g.Review(merged, "bob", review.Approve); !errors.Is(err, ErrState) {
		t.Fatalf("self-review on merged = %v", err)
	}
	// 自评（RequestChanges 同理）。
	if err := g.Review(id, "bob", review.Approve); !errors.Is(err, ErrSelfReview) {
		t.Fatalf("self approve = %v", err)
	}
	if err := g.Review(id, "bob", review.RequestChanges); !errors.Is(err, ErrSelfReview) {
		t.Fatalf("self request changes = %v", err)
	}
	// 作者自己 Comment 合法。
	if err := g.Review(id, "bob", review.Comment); err != nil {
		t.Fatalf("author comment: %v", err)
	}

	// Dismiss：admin 清除无裁决者报状态不符；非 admin 报无权限。
	if err := g.Dismiss(id, "nobody", "admin"); !errors.Is(err, ErrState) {
		t.Fatalf("dismiss absent verdict = %v", err)
	}
	if err := g.Dismiss(id, "carol", "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("dismiss by writer = %v", err)
	}

	// Ready 非草稿 / UpdateBranch 已最新报状态不符。
	if err := g.Ready(id); !errors.Is(err, ErrState) {
		t.Fatalf("ready non-draft = %v", err)
	}
	fresh, _ := g.Open("bob", []string{"docs/e.md"}, false)
	if err := g.UpdateBranch(fresh); !errors.Is(err, ErrState) {
		t.Fatalf("update up-to-date = %v", err)
	}

	// Report head 超前报状态不符。
	if err := g.Report(id, "build", 99, review.Success); !errors.Is(err, ErrState) {
		t.Fatalf("future head report = %v", err)
	}
}

// TestRejectionNoStateChange 被拒绝的操作不改变可观察状态。
func TestRejectionNoStateChange(t *testing.T) {
	g := exampleGate()
	id, _ := g.Open("bob", []string{"main.go"}, false)
	_, head, merged, draft, ok := g.BaseHead(id)
	if !ok || head != 1 || merged || draft {
		t.Fatal("unexpected initial state")
	}
	_ = g.Push(id, nil)                     // 参数非法
	_ = g.Push(999, []string{"x"})          // 不存在
	_ = g.Review(id, "bob", review.Approve) // 自评
	_ = g.Review(999, "x", review.Approve)  // 不存在
	_ = g.Report(999, "build", 1, review.Failure)
	base2, head2, merged2, draft2, ok2 := g.BaseHead(id)
	if !ok2 || base2 != 0 || head2 != 1 || merged2 || draft2 {
		t.Fatalf("state changed after rejected ops: %d %d %v %v", base2, head2, merged2, draft2)
	}
}

// TestConcurrentSmoke 高并发混合操作下以 -race 验证可串行化的数据结构安全。
func TestConcurrentSmoke(t *testing.T) {
	cfg := Config{N: 1, DismissStale: true, RequireOwners: false, Strict: true, Required: []string{"ci"}}
	g := New(cfg, []owners.Rule{{Pattern: "*", Owners: []string{"alice"}}})
	for _, u := range []string{"a", "b", "c"} {
		if err := g.SetPerm(u, review.Write); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id, err := g.Open("a", []string{"f"}, false)
				if err != nil {
					t.Error(err)
					return
				}
				_ = g.Review(id, "b", review.Approve)
				_ = g.Report(id, "ci", 1, review.Success)
				_ = g.Mergeable(id)
				if err := g.Merge(id, "a"); err == nil {
					if g.T() < 1 {
						t.Error("T not advanced after merge")
					}
				}
				_ = g.Push(id, []string{"g"})
				_ = g.Ready(id)
				_ = g.UpdateBranch(id)
				_ = g.Dismiss(id, "b", "a")
			}
		}(w)
	}
	wg.Wait()

	// T 必须等于已合并请求数。
	mergedCount := 0
	for id := 1; ; id++ {
		_, _, merged, _, ok := g.BaseHead(id)
		if !ok {
			break
		}
		if merged {
			mergedCount++
		}
	}
	if g.T() != mergedCount {
		t.Fatalf("T=%d but merged PRs=%d", g.T(), mergedCount)
	}
}
