package ontology

import (
	"errors"
	"testing"
)

func mustRegister(t *testing.T, store *Store, name string, action DeleteAction, preserve bool) {
	t.Helper()
	if err := store.RegisterLinkType(LinkTypeConfig{Name: name, OnDelete: action, PreserveOnExists: preserve}); err != nil {
		t.Fatal(err)
	}
}

func mustCreate(t *testing.T, store *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := store.CreateObject(id); err != nil {
			t.Fatal(err)
		}
	}
}

func mustLink(t *testing.T, store *Store, typ, source, target string) {
	t.Helper()
	if err := store.AddLink(Link{Type: typ, Source: source, Target: target}); err != nil {
		t.Fatal(err)
	}
}

func assertObjects(t *testing.T, store *Store, want []string, gone []string) {
	t.Helper()
	for _, id := range want {
		if !store.HasObject(id) {
			t.Fatalf("object %q should exist", id)
		}
	}
	for _, id := range gone {
		if store.HasObject(id) {
			t.Fatalf("object %q should have been deleted", id)
		}
	}
}

func assertLinks(t *testing.T, got []Link, want []Link) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d links %v, want %d links %v", len(got), got, len(want), want)
	}
	gotByKey := map[string]Link{}
	for _, link := range got {
		gotByKey[linkKey(link)] = link
	}
	for _, link := range want {
		if _, ok := gotByKey[linkKey(link)]; !ok {
			t.Fatalf("missing link %v; got %v", link, got)
		}
	}
}

func TestCascadeCycleTerminatesAndDeletesOnce(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "next", Cascade, false)
	mustCreate(t, store, "a", "b", "c")
	mustLink(t, store, "next", "a", "b")
	mustLink(t, store, "next", "b", "c")
	mustLink(t, store, "next", "c", "a")

	result, err := store.DeleteObject("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DeletedObjects) != 3 {
		t.Fatalf("deleted = %v, want a,b,c", result.DeletedObjects)
	}
	assertObjects(t, store, nil, []string{"a", "b", "c"})
	if len(store.Links()) != 0 {
		t.Fatalf("links = %v, want none", store.Links())
	}

	cascadeSteps := 0
	for _, step := range result.Steps {
		if step.Kind == "cascade-peer" {
			cascadeSteps++
		}
	}
	if cascadeSteps != 2 {
		t.Fatalf("cascade scheduling steps = %d, want 2 due to visited-set dedupe", cascadeSteps)
	}
}

func TestMixedRulesRejectEntireTransactionAndRestoreState(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "owner", Cascade, true)
	mustRegister(t, store, "tag", SetNull, false)
	mustRegister(t, store, "membership", Restrict, false)
	mustCreate(t, store, "root", "owned", "kept", "member", "external")
	mustLink(t, store, "owner", "root", "owned")
	mustLink(t, store, "tag", "root", "kept")
	mustLink(t, store, "membership", "root", "member")
	mustLink(t, store, "membership", "external", "member")

	before := store.Links()
	_, err := store.DeleteObject("root")
	if !errors.Is(err, ErrDeleteRestricted) {
		t.Fatalf("err = %v, want ErrDeleteRestricted", err)
	}

	assertObjects(t, store, []string{"root", "owned", "kept", "member", "external"}, nil)
	assertLinks(t, store.Links(), before)

	logs := store.Logs()
	last := logs[len(logs)-1]
	if last.Committed {
		t.Fatal("failed delete must not be logged as committed")
	}
	if len(last.Steps) == 0 {
		t.Fatal("failed delete must retain the rules used during planning")
	}
}

func TestOrphanCleanupRoundsCascade(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "holds", Cascade, true)
	mustRegister(t, store, "keeps", Cascade, true)
	mustCreate(t, store, "root", "child", "grandchild", "survivor")
	mustLink(t, store, "holds", "root", "child")
	mustLink(t, store, "keeps", "child", "grandchild")

	result, err := store.DeleteObject("root")
	if err != nil {
		t.Fatal(err)
	}
	assertObjects(t, store, []string{"survivor"}, []string{"root", "child", "grandchild"})
	assertLinks(t, store.Links(), nil)

	deleted := map[string]bool{}
	for _, id := range result.DeletedObjects {
		deleted[id] = true
	}
	if !deleted["child"] || !deleted["grandchild"] {
		t.Fatalf("deleted = %v, want cascade child and orphan grandchild", result.DeletedObjects)
	}
}

func TestSetNullPreservesPeerAndRemovesLink(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "tag", SetNull, false)
	mustCreate(t, store, "root", "peer")
	mustLink(t, store, "tag", "root", "peer")

	result, err := store.DeleteObject("root")
	if err != nil {
		t.Fatal(err)
	}
	assertObjects(t, store, []string{"peer"}, []string{"root"})
	if len(result.NullifiedLinks) != 1 || result.NullifiedLinks[0].Target != "peer" {
		t.Fatalf("nullified = %v, want root->peer tag", result.NullifiedLinks)
	}
	if len(result.RemovedLinks) != 0 {
		t.Fatalf("removed = %v, set-null must be reported separately", result.RemovedLinks)
	}
	assertLinks(t, store.Links(), nil)
}

func TestErrorPriority(t *testing.T) {
	store := NewStore()
	_, err := store.DeleteObject("missing")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing object error = %v", err)
	}

	mustCreate(t, store, "a", "b", "c", "d", "e")
	store.loadRawLinkForTest(Link{Type: "unknown", Source: "a", Target: "b"})
	mustRegister(t, store, "block", Restrict, false)
	mustLink(t, store, "block", "c", "a")
	mustLink(t, store, "block", "a", "d")
	mustLink(t, store, "block", "e", "d")
	_, err = store.DeleteObject("a")
	if !errors.Is(err, ErrDeleteRestricted) {
		t.Fatalf("restrict should precede undefined, got %v", err)
	}

	store2 := NewStore()
	mustRegister(t, store2, "block", Restrict, false)
	mustCreate(t, store2, "a", "b", "c")
	store2.loadRawLinkForTest(Link{Type: "unknown", Source: "a", Target: "c"})
	mustLink(t, store2, "block", "a", "b")
	_, err = store2.DeleteObject("a")
	if !errors.Is(err, ErrUndefinedLinkType) {
		t.Fatalf("undefined error = %v", err)
	}
}
