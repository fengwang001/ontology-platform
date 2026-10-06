package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func loadCommit(t *testing.T, svc *Service, input CommitInput) {
	t.Helper()
	if err := svc.Load(input); err != nil {
		t.Fatalf("input=%#v actual=%v criterion=load succeeds", input, err)
	}
	t.Logf("input=%#v actual=loaded criterion=accepted commit", input)
}

func blame(t *testing.T, svc *Service, id, path string, version int, want []Attribution) {
	t.Helper()
	got, err := svc.Blame(id, path, version)
	if err != nil {
		t.Fatalf("input=(%q,%q,%d) actual=%v criterion=blame succeeds", id, path, version, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input=(%q,%q,%d) actual=%#v criterion=equals %#v", id, path, version, got, want)
	}
	t.Logf("input=(%q,%q,%d) actual=%#v criterion=row-by-row attribution", id, path, version, got)
}

func attr(id, path string, line int, ignored bool) Attribution {
	return Attribution{CommitID: id, Path: path, Line: line, Ignored: ignored}
}

func TestLinearAndMergeBlame(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "base", Files: map[string]string{"f": "a\nb\n"}})
	loadCommit(t, svc, CommitInput{
		ID:        "left",
		ParentIDs: []string{"base"},
		Files:     map[string]string{"f": "a\nx\n"},
	})
	loadCommit(t, svc, CommitInput{
		ID:        "right",
		ParentIDs: []string{"base"},
		Files:     map[string]string{"f": "a\ny\n"},
	})
	loadCommit(t, svc, CommitInput{
		ID:        "merge",
		ParentIDs: []string{"left", "right"},
		Files:     map[string]string{"f": "a\nx\ny\n"},
	})
	loadCommit(t, svc, CommitInput{
		ID:        "after",
		ParentIDs: []string{"merge"},
		Files:     map[string]string{"f": "a\nc\nb\n"},
	})

	blame(t, svc, "base", "f", 0, []Attribution{
		attr("base", "f", 1, false),
		attr("base", "f", 2, false),
	})
	blame(t, svc, "merge", "f", 0, []Attribution{
		attr("base", "f", 1, false),
		attr("left", "f", 2, false),
		attr("right", "f", 2, false),
	})
	blame(t, svc, "after", "f", 0, []Attribution{
		attr("base", "f", 1, false),
		attr("after", "f", 2, false),
		attr("after", "f", 3, false),
	})
	t.Log("input=merge snapshot with a in both parents and after drops merge-only context actual=base via first parent criterion=first-parent and LCS order")
}

func TestRenamesThroughContinuousAndBack(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "root", Files: map[string]string{"old": "a\n"}})
	loadCommit(t, svc, CommitInput{
		ID:        "one",
		ParentIDs: []string{"root"},
		Files:     map[string]string{"middle": "a\nb\n"},
		Renames:   []Rename{{OldPath: "old", NewPath: "middle"}},
	})
	loadCommit(t, svc, CommitInput{
		ID:        "two",
		ParentIDs: []string{"one"},
		Files:     map[string]string{"new": "a\nb\nc\n"},
		Renames:   []Rename{{OldPath: "middle", NewPath: "new"}},
	})
	loadCommit(t, svc, CommitInput{
		ID:        "back",
		ParentIDs: []string{"two"},
		Files:     map[string]string{"old": "a\nb\nc\n"},
		Renames:   []Rename{{OldPath: "new", NewPath: "old"}},
	})

	blame(t, svc, "one", "middle", 0, []Attribution{
		attr("root", "old", 1, false),
		attr("one", "middle", 2, false),
	})
	blame(t, svc, "two", "new", 0, []Attribution{
		attr("root", "old", 1, false),
		attr("one", "middle", 2, false),
		attr("two", "new", 3, false),
	})
	blame(t, svc, "back", "old", 0, []Attribution{
		attr("root", "old", 1, false),
		attr("one", "middle", 2, false),
		attr("two", "new", 3, false),
	})
}

func TestMergeRenameFromLaterParent(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "source", Files: map[string]string{"src": "keep\nfrom-source\n"}})
	loadCommit(t, svc, CommitInput{ID: "side", ParentIDs: []string{"source"}, Files: map[string]string{"other": "side\n"}})
	loadCommit(t, svc, CommitInput{
		ID:        "merge-rename",
		ParentIDs: []string{"side", "source"},
		Files:     map[string]string{"other": "side\n", "dst": "keep\nfrom-source\nnew\n"},
		Renames:   []Rename{{OldPath: "src", NewPath: "dst"}},
	})
	blame(t, svc, "merge-rename", "dst", 0, []Attribution{
		attr("source", "src", 1, false),
		attr("source", "src", 2, false),
		attr("merge-rename", "dst", 3, false),
	})
}

func TestIgnoreListSingleAndMultiplePenetration(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "root", Files: map[string]string{"f": "a\n"}})
	loadCommit(t, svc, CommitInput{ID: "bad1", ParentIDs: []string{"root"}, Files: map[string]string{"f": "a\nb\n"}})
	loadCommit(t, svc, CommitInput{ID: "bad2", ParentIDs: []string{"bad1"}, Files: map[string]string{"f": "a\nb\nc\n"}})
	loadCommit(t, svc, CommitInput{ID: "good", ParentIDs: []string{"bad2"}, Files: map[string]string{"f": "a\nb\nc\n"}})

	version, err := svc.AddIgnoreList([]string{"bad1", "bad2"})
	if err != nil || version != 1 {
		t.Fatalf("input=[bad1 bad2] actual=(%d,%v) criterion=version 1", version, err)
	}
	t.Logf("input=[bad1 bad2] actual=version %d criterion=versioned ignore list", version)
	blame(t, svc, "bad1", "f", 1, []Attribution{
		attr("root", "f", 1, false),
		attr("bad1", "f", 2, true),
	})
	blame(t, svc, "bad2", "f", 1, []Attribution{
		attr("root", "f", 1, false),
		attr("bad1", "f", 2, true),
		attr("bad2", "f", 3, true),
	})
	blame(t, svc, "good", "f", 1, []Attribution{
		attr("root", "f", 1, false),
		attr("bad1", "f", 2, true),
		attr("bad2", "f", 3, true),
	})
}

func TestRejectionOrderAndAtomicLoad(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "exists", Files: map[string]string{"present": ""}})

	cases := []struct {
		name  string
		input CommitInput
		want  error
	}{
		{"parent missing", CommitInput{ID: "x", ParentIDs: []string{"missing"}}, ErrParentNotFound},
		{"rename old missing", CommitInput{ID: "x", ParentIDs: []string{"exists"}, Files: map[string]string{"n": ""}, Renames: []Rename{{OldPath: "missing", NewPath: "n"}}}, ErrInvalidRename},
		{"rename old remains", CommitInput{ID: "x", ParentIDs: []string{"exists"}, Files: map[string]string{"present": "", "n": ""}, Renames: []Rename{{OldPath: "present", NewPath: "n"}}}, ErrInvalidRename},
		{"duplicate rename target", CommitInput{ID: "x", ParentIDs: []string{"exists"}, Files: map[string]string{"a": "", "b": "", "c": ""}, Renames: []Rename{{OldPath: "present", NewPath: "n"}, {OldPath: "a", NewPath: "n"}}}, ErrInvalidRename},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Load(tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("input=%#v actual=%v criterion=%v", tc.input, err, tc.want)
			}
			if _, err := svc.Blame(tc.input.ID, "present", 0); !errors.Is(err, ErrCommitNotFound) {
				t.Fatalf("input=%#v actual=%v criterion=rejected load leaves no commit", tc.input, err)
			}
			t.Logf("input=%#v actual=%v criterion=rejection and no trace", tc.input, err)
		})
	}

	queryCases := []struct {
		name   string
		id     string
		path   string
		ver    int
		listOK bool
		want   error
	}{
		{"invalid before commit", "", "missing", 99, false, ErrInvalidArgument},
		{"commit before list", "missing", "missing", 99, false, ErrCommitNotFound},
		{"list before path", "exists", "missing", 99, false, ErrListVersionNotFound},
		{"path last", "exists", "missing", 0, true, ErrPathNotFound},
	}
	for _, tc := range queryCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Blame(tc.id, tc.path, tc.ver)
			if !errors.Is(err, tc.want) {
				t.Fatalf("input=(%q,%q,%d) actual=%v criterion=%v", tc.id, tc.path, tc.ver, err, tc.want)
			}
			t.Logf("input=(%q,%q,%d) actual=%v criterion=ordered error", tc.id, tc.path, tc.ver, err)
		})
	}
}
