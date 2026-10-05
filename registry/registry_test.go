package registry

import (
	"errors"
	"testing"
)

func allPerms(stages ...string) Caller {
	c := Caller{}
	for _, s := range stages {
		for _, a := range []Action{Push, Promote, Yank, Alias} {
			c[Permission{Action: a, Stage: s}] = true
		}
	}
	return c
}

func mutableStages() []Stage {
	return []Stage{
		{Name: "dev"},
		{Name: "prod", Immutable: true, S: 10},
	}
}

func immutableStages() []Stage {
	return []Stage{
		{Name: "main", Immutable: true},
		{Name: "archive", Immutable: true, S: 5},
	}
}

func mustNew(t *testing.T, stages []Stage) *Repo {
	t.Helper()
	r, err := New(stages)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func checkErr(t *testing.T, what string, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s: got %v, want nil", what, got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// step 是一条表驱动操作：op 为 push/yank/alias/resolve。
// alias 时 tag 字段是别名、digest 字段是目标标签；resolve 时 tag 字段是 ref。
type step struct {
	op      string
	now     int64
	stage   string
	name    string
	tag     string
	digest  string
	caller  Caller
	want    error
	wantDig string
}

func runSteps(t *testing.T, r *Repo, steps []step) {
	t.Helper()
	for i, s := range steps {
		if s.name == "" {
			s.name = "app"
		}
		if s.tag == "" {
			s.tag = "v1"
		}
		var err error
		switch s.op {
		case "push":
			err = r.Push(s.now, s.name, s.tag, s.digest, s.caller)
		case "yank":
			err = r.Yank(s.now, s.stage, s.name, s.tag, s.caller)
		case "alias":
			err = r.SetAlias(s.now, s.stage, s.name, s.tag, s.digest, s.caller)
		case "resolve":
			var got string
			got, err = r.Resolve(s.stage, s.name, s.tag)
			if err == nil && got != s.wantDig {
				t.Fatalf("step %d resolve: got digest %q, want %q", i, got, s.wantDig)
			}
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
		if s.want == nil {
			if err != nil {
				t.Fatalf("step %d %s: got err %v, want nil", i, s.op, err)
			}
		} else if !errors.Is(err, s.want) {
			t.Fatalf("step %d %s: got err %v, want %v", i, s.op, err, s.want)
		}
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name   string
		stages []Stage
		want   error
	}{
		{"ok-2", mutableStages(), nil},
		{"too-few", []Stage{{Name: "a"}}, ErrInvalidArgument},
		{"too-many", []Stage{
			{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"},
			{Name: "f"}, {Name: "g"}, {Name: "h"}, {Name: "i"},
		}, ErrInvalidArgument},
		{"empty-name", []Stage{{Name: ""}, {Name: "b"}}, ErrInvalidArgument},
		{"dup-name", []Stage{{Name: "a"}, {Name: "a"}}, ErrInvalidArgument},
		{"first-S-nonzero", []Stage{{Name: "a", S: 1}, {Name: "b"}}, ErrInvalidArgument},
		{"first-required", []Stage{{Name: "a", Required: []string{"test"}}, {Name: "b"}}, ErrInvalidArgument},
		{"S-negative", []Stage{{Name: "a"}, {Name: "b", S: -1}}, ErrInvalidArgument},
		{"S-too-big", []Stage{{Name: "a"}, {Name: "b", S: 1_000_000_001}}, ErrInvalidArgument},
		{"S-max-ok", []Stage{{Name: "a"}, {Name: "b", S: 1_000_000_000}}, nil},
		{"required-dup", []Stage{{Name: "a"}, {Name: "b", Required: []string{"x", "x"}}}, ErrInvalidArgument},
		{"required-too-many", []Stage{{Name: "a"}, {Name: "b",
			Required: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}}}, ErrInvalidArgument},
		{"eight-ok", []Stage{
			{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"},
			{Name: "e"}, {Name: "f"}, {Name: "g"}, {Name: "h"},
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.stages)
			checkErr(t, "New", err, tc.want)
		})
	}
}

func TestPushLandingRules(t *testing.T) {
	full := allPerms("dev", "prod", "main", "archive")
	cases := []struct {
		name   string
		stages []Stage
		steps  []step
	}{
		{
			name:   "mutable overwrite and resolve",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "resolve", stage: "dev", wantDig: "d1"},
				{op: "push", now: 1, digest: "d2", caller: full},
				{op: "resolve", stage: "dev", wantDig: "d2"},
			},
		},
		{
			name:   "immutable create, idempotent, conflict",
			stages: immutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "push", now: 1, digest: "d1", caller: full}, // 幂等成功
				{op: "push", now: 2, digest: "d2", caller: full, want: ErrImmutable},
				{op: "resolve", stage: "main", wantDig: "d1"},
			},
		},
		{
			name:   "tombstone rejects any digest",
			stages: immutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "yank", now: 1, stage: "main", caller: full},
				{op: "resolve", stage: "main", want: ErrYanked},
				{op: "push", now: 2, digest: "d1", caller: full, want: ErrYanked},
				{op: "push", now: 3, digest: "d2", caller: full, want: ErrYanked},
			},
		},
		{
			name:   "mutable yank deletes and allows reuse",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "yank", now: 1, stage: "dev", caller: full},
				{op: "resolve", stage: "dev", want: ErrTagNotFound},
				{op: "push", now: 2, digest: "d2", caller: full},
				{op: "resolve", stage: "dev", wantDig: "d2"},
			},
		},
		{
			name:   "tag name collides with alias",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "alias", now: 1, stage: "dev", tag: "stable", digest: "v1", caller: full},
				{op: "push", now: 2, tag: "stable", digest: "d2", caller: full, want: ErrAliasConflict},
			},
		},
		{
			name:   "push rejects bad args before clock",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 10, digest: "d1", caller: full},
				{op: "push", now: 5, digest: "", caller: full, want: ErrInvalidArgument},
				{op: "push", now: -1, digest: "d2", caller: full, want: ErrInvalidArgument},
				{op: "push", now: MaxNow + 1, digest: "d2", caller: full, want: ErrInvalidArgument},
				{op: "push", now: 5, digest: "d2", caller: full, want: ErrClockRewind},
				{op: "push", now: 10, digest: "d2", caller: full}, // 恰等允许
				{op: "resolve", stage: "dev", wantDig: "d2"},
			},
		},
		{
			name:   "permission denied does not advance clock",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 5, digest: "d1", caller: Caller{}, want: ErrPermissionDenied},
				{op: "push", now: 5, digest: "d1", caller: allPerms("prod"), want: ErrPermissionDenied},
				{op: "push", now: 5, digest: "d1", caller: full},
				{op: "resolve", stage: "dev", wantDig: "d1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSteps(t, mustNew(t, tc.stages), tc.steps)
		})
	}
}

func TestYankAndAlias(t *testing.T) {
	full := allPerms("dev", "prod", "main", "archive")
	cases := []struct {
		name   string
		stages []Stage
		steps  []step
	}{
		{
			name:   "yank referenced by alias",
			stages: immutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "alias", now: 1, stage: "main", tag: "stable", digest: "v1", caller: full},
				{op: "yank", now: 2, stage: "main", caller: full, want: ErrReferenced},
				{op: "resolve", stage: "main", wantDig: "d1"},
			},
		},
		{
			name:   "yank missing and already yanked",
			stages: immutableStages(),
			steps: []step{
				{op: "yank", now: 0, stage: "main", caller: full, want: ErrTagNotFound},
				{op: "push", now: 1, digest: "d1", caller: full},
				{op: "yank", now: 2, stage: "main", caller: full},
				{op: "yank", now: 3, stage: "main", caller: full, want: ErrYanked},
			},
		},
		{
			name:   "yank unknown stage and permission order",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 10, digest: "d1", caller: full},
				{op: "yank", now: 5, stage: "nowhere", caller: full, want: ErrInvalidArgument},
				{op: "yank", now: 5, stage: "dev", caller: full, want: ErrClockRewind},
				{op: "yank", now: 11, stage: "dev", caller: Caller{}, want: ErrPermissionDenied},
				{op: "yank", now: 11, stage: "dev", caller: full},
			},
		},
		{
			name:   "alias to missing or yanked tag",
			stages: immutableStages(),
			steps: []step{
				{op: "alias", now: 0, stage: "main", tag: "a1", digest: "ghost", caller: full, want: ErrTagNotFound},
				{op: "push", now: 1, digest: "d1", caller: full},
				{op: "yank", now: 2, stage: "main", caller: full},
				{op: "alias", now: 3, stage: "main", tag: "a1", digest: "v1", caller: full, want: ErrYanked},
			},
		},
		{
			name:   "alias conflicts with tag and tombstone, repoint ok",
			stages: immutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "push", now: 1, tag: "v2", digest: "d2", caller: full},
				{op: "alias", now: 2, stage: "main", tag: "v1", digest: "v2", caller: full, want: ErrAliasConflict},
				{op: "yank", now: 3, stage: "main", tag: "v2", caller: full},
				{op: "alias", now: 4, stage: "main", tag: "v2", digest: "v1", caller: full, want: ErrAliasConflict},
				{op: "alias", now: 5, stage: "main", tag: "stable", digest: "v1", caller: full},
				{op: "resolve", stage: "main", tag: "stable", wantDig: "d1"},
			},
		},
		{
			name:   "alias repoint changes resolution",
			stages: mutableStages(),
			steps: []step{
				{op: "push", now: 0, digest: "d1", caller: full},
				{op: "push", now: 1, tag: "v2", digest: "d2", caller: full},
				{op: "alias", now: 2, stage: "dev", tag: "stable", digest: "v1", caller: full},
				{op: "resolve", stage: "dev", tag: "stable", wantDig: "d1"},
				{op: "alias", now: 3, stage: "dev", tag: "stable", digest: "v2", caller: full},
				{op: "resolve", stage: "dev", tag: "stable", wantDig: "d2"},
			},
		},
		{
			name:   "resolve unknown stage and missing ref",
			stages: mutableStages(),
			steps: []step{
				{op: "resolve", stage: "nowhere", want: ErrInvalidArgument},
				{op: "resolve", stage: "dev", want: ErrTagNotFound},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSteps(t, mustNew(t, tc.stages), tc.steps)
		})
	}
}

func TestResolveScansAtMostTwo(t *testing.T) {
	r := mustNew(t, mutableStages())
	full := allPerms("dev")
	steps := []step{
		{op: "push", now: 0, digest: "d1", caller: full},
		{op: "alias", now: 1, stage: "dev", tag: "stable", digest: "v1", caller: full},
	}
	runSteps(t, r, steps)

	r.scanned = 0
	if _, err := r.Resolve("dev", "app", "stable"); err != nil {
		t.Fatal(err)
	}
	if r.scanned > 2 {
		t.Fatalf("alias resolve scanned %d records, want <= 2", r.scanned)
	}

	r.scanned = 0
	if _, err := r.Resolve("dev", "app", "v1"); err != nil {
		t.Fatal(err)
	}
	if r.scanned > 2 {
		t.Fatalf("tag resolve scanned %d records, want <= 2", r.scanned)
	}

	r.scanned = 0
	if _, err := r.Resolve("dev", "app", "ghost"); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("got %v, want ErrTagNotFound", err)
	}
	if r.scanned > 2 {
		t.Fatalf("miss resolve scanned %d records, want <= 2", r.scanned)
	}
}
