package layerconfig_test

import (
	"testing"

	"ontology/layerconfig"
)

// TestRequiredPropagation verifies that introducing an environment/region/
// instance identity forces required keys to resolve everywhere in that chain.
func TestRequiredPropagation(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{
		Key: "req", Type: layerconfig.TypeString, Required: true, Merge: layerconfig.MergeOverride,
	}, l)
	mustRegister(t, s, layerconfig.Schema{
		Key: "opt", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
	}, l)

	// No identities yet: a global write of the required key is valid.
	mustPublish(t, s, l, "global required value; still no named identity",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "req", Value: layerconfig.Value{Str: "g"}},
	)
	// Creating an env identity with an optional-only write must fail: the env
	// has no required value reachable (global actually does... so use cancel).
	failPublishKind(t, s, l, layerconfig.KindRequiredMissing,
		"env cancellation creates an identity where required key is unset",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "p"}, Key: "req"},
	)
	// A region instance created by an optional write inherits global required,
	// so it is valid.
	mustPublish(t, s, l, "optional env write creates identity; global required covers it",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "p"}, Key: "opt", Value: layerconfig.Value{Str: "x"}},
	)
	// A region cancel of the required key breaks the region and any instance.
	failPublishKind(t, s, l, layerconfig.KindRequiredMissing,
		"region cancel makes required key unset for that region identity",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "p", Region: "cn"}, Key: "req"},
	)
	// Adding a region value fixes it for the region; an instance cancel then
	// breaks the instance but not the region.
	failPublishKind(t, s, l, layerconfig.KindRequiredMissing,
		"instance cancel breaks the instance identity",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "p", Region: "cn", Instance: "i1"}, Key: "req"},
	)
	mustPublish(t, s, l, "region value and instance value together satisfy the chain",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "p", Region: "cn"}, Key: "req", Value: layerconfig.Value{Str: "r"}},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "p", Region: "cn", Instance: "i1"}, Key: "req", Value: layerconfig.Value{Str: "i"}},
	)
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "p", Region: "cn", Instance: "i1"}, "req",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "i"}}, "instance value in force")

	// Clearing the last write that defines an identity removes the identity,
	// relaxing required validation again.
	mustPublish(t, s, l, "clear optional env write: env identity still exists via region/instance writes",
		layerconfig.Change{Op: layerconfig.OpClear, Scope: layerconfig.Scope{Env: "p"}, Key: "opt"},
	)
	// Now remove all writes under the p/cn/i1 chain.
	mustPublish(t, s, l, "clear region and instance required writes",
		layerconfig.Change{Op: layerconfig.OpClear, Scope: layerconfig.Scope{Env: "p", Region: "cn"}, Key: "req"},
		layerconfig.Change{Op: layerconfig.OpClear, Scope: layerconfig.Scope{Env: "p", Region: "cn", Instance: "i1"}, Key: "req"},
	)
	// With those identities gone, global is still valid; current version
	// simply has only global state.
	checkResolve(t, s, l, -1, layerconfig.Scope{}, "req",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "g"}}, "global value remains")
}

// TestErrorPriority ensures the documented error-kind priority is honored.
func TestErrorPriority(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{Key: "k", Type: layerconfig.TypeInt, Min: 0, Max: 5, Merge: layerconfig.MergeOverride}, l)

	// Invalid argument beats key-not-registered in the same batch.
	failPublishKind(t, s, l, layerconfig.KindInvalidArgument,
		"bad scope must outrank unknown key",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Region: "r"}, Key: "ghost", Value: layerconfig.Value{Int: 1}},
	)
	// Key-not-registered beats type/range.
	failPublishKind(t, s, l, layerconfig.KindKeyNotRegistered,
		"unknown key outranks everything after arguments",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "ghost", Value: layerconfig.Value{Int: 1}},
	)
	// Type/range beats batch conflict.
	failPublishKind(t, s, l, layerconfig.KindTypeOrRange,
		"out-of-range int outranks duplicate-target batch conflict",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "k", Value: layerconfig.Value{Int: 99}},
		layerconfig.Change{Op: layerconfig.OpClear, Scope: layerconfig.Scope{}, Key: "k"},
	)
	// Batch conflict beats lock conflict.
	mustPublish(t, s, l, "seed global lock after narrowing cleared",
		layerconfig.Change{Op: layerconfig.OpLock, Scope: layerconfig.Scope{}, Key: "k"},
	)
	failPublishKind(t, s, l, layerconfig.KindBatchConflict,
		"same-target duplicates outrank lock conflict",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e"}, Key: "k", Value: layerconfig.Value{Int: 1}},
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "e"}, Key: "k"},
	)

	// Version-not-found outranks key-not-registered for reads.
	if _, err := s.Resolve(999, layerconfig.Scope{Env: "e"}, "ghost"); kindOf(err) != layerconfig.KindVersionNotFound {
		t.Fatalf("read priority: want VersionNotFound, got %v", err)
	}
	l.line("read: missing version for unknown key -> VersionNotFound (basis: version precedes key)")

	// Invalid argument beats version-not-found.
	if _, err := s.Resolve(999, layerconfig.Scope{Region: "r"}, "k"); kindOf(err) != layerconfig.KindInvalidArgument {
		t.Fatalf("read priority: want InvalidArgument, got %v", err)
	}
	l.line("read: invalid scope with missing version -> InvalidArgument (basis: arguments first)")
}

func kindOf(err error) layerconfig.ErrorKind {
	if e, ok := layerconfig.AsError(err); ok {
		return e.Kind
	}
	return -1
}

// TestRollbackAndHistory verifies rollback creates a new identical-content
// version, current-version rollback is a no-op, missing versions error, and
// historical reads work.
func TestRollbackAndHistory(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{Key: "v", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride}, l)

	v1 := mustPublish(t, s, l, "version 1 value a",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "v", Value: layerconfig.Value{Str: "a"}},
	)
	v2 := mustPublish(t, s, l, "version 2 value b",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "v", Value: layerconfig.Value{Str: "b"}},
	)
	if v1 != 1 || v2 != 2 {
		t.Fatalf("versions: got %d,%d want 1,2", v1, v2)
	}

	// Historical reads use the pinned content.
	checkResolve(t, s, l, 1, layerconfig.Scope{}, "v",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "a"}}, "version 1 read")
	checkResolve(t, s, l, 2, layerconfig.Scope{}, "v",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "b"}}, "version 2 read")

	// Roll back to version 1: new version 3 with identical content, and
	// versions 1 and 2 remain readable.
	v3, err := s.Rollback(1)
	l.logRollback(1, v3, err, "rollback creates a new version with v1 content")
	if err != nil || v3 != 3 {
		t.Fatalf("rollback: v=%d err=%v", v3, err)
	}
	checkResolve(t, s, l, -1, layerconfig.Scope{}, "v",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "a"}}, "current content equals v1")
	checkResolve(t, s, l, 2, layerconfig.Scope{}, "v",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "b"}}, "v2 retained and readable")

	// Rolling back to current is a no-op and allocates no version.
	before := s.CurrentVersion()
	v, err := s.Rollback(before)
	l.logRollback(before, v, err, "rollback to current is a no-op")
	if err != nil || v != before || s.CurrentVersion() != before {
		t.Fatalf("current rollback must be no-op")
	}

	// Missing versions error.
	if _, err := s.Rollback(99); kindOf(err) != layerconfig.KindVersionNotFound {
		t.Fatalf("want VersionNotFound, got %v", err)
	}
	l.logRollback(99, s.CurrentVersion(), err, "missing rollback target errors")

	// Historical read of the rollback version 3 shows a content.
	checkResolve(t, s, l, 3, layerconfig.Scope{}, "v",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "a"}}, "v3 is the rollback content")
}

// TestVersionZero verifies the initial state is an empty version 0 with no
// values; successful publishes increment one at a time.
func TestVersionZero(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	if s.CurrentVersion() != 0 {
		t.Fatalf("initial version must be 0")
	}
	mustRegister(t, s, layerconfig.Schema{Key: "k", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride}, l)
	checkResolve(t, s, l, 0, layerconfig.Scope{}, "k", layerconfig.Unset, "version 0 is empty")
	failPublishKind(t, s, l, layerconfig.KindInvalidArgument, "empty batch rejected")
}
