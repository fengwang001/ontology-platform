package layerconfig_test

import (
	"testing"

	"ontology/layerconfig"
)

// TestLockSemantics verifies: pre-existing narrower writes block a new lock;
// the locking layer itself may still write; narrower write/cancel are
// rejected; env locks scope to their env; unlock restores writes.
func TestLockSemantics(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{
		Key: "k", Type: layerconfig.TypeInt, Min: 0, Max: 100, Merge: layerconfig.MergeOverride,
	}, l)

	mustPublish(t, s, l, "instance write exists before lock",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e", Region: "r", Instance: "i"}, Key: "k", Value: layerconfig.Value{Int: 1}},
	)
	failPublishKind(t, s, l, layerconfig.KindLockConflict,
		"new global lock rejected while narrower write exists",
		layerconfig.Change{Op: layerconfig.OpLock, Scope: layerconfig.Scope{}, Key: "k"},
	)
	mustPublish(t, s, l, "clear the narrower write first",
		layerconfig.Change{Op: layerconfig.OpClear, Scope: layerconfig.Scope{Env: "e", Region: "r", Instance: "i"}, Key: "k"},
	)
	mustPublish(t, s, l, "global lock now succeeds",
		layerconfig.Change{Op: layerconfig.OpLock, Scope: layerconfig.Scope{}, Key: "k"},
	)
	mustPublish(t, s, l, "locking (global) layer may write itself",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "k", Value: layerconfig.Value{Int: 7}},
	)
	failPublishKind(t, s, l, layerconfig.KindLockConflict, "narrow write under lock",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e"}, Key: "k", Value: layerconfig.Value{Int: 2}},
	)
	failPublishKind(t, s, l, layerconfig.KindLockConflict, "narrow cancel under lock",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "e"}, Key: "k"},
	)
	mustPublish(t, s, l, "unlock globally",
		layerconfig.Change{Op: layerconfig.OpUnlock, Scope: layerconfig.Scope{}, Key: "k"},
	)
	mustPublish(t, s, l, "narrow write after unlock",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e"}, Key: "k", Value: layerconfig.Value{Int: 9}},
	)

	// Env-level lock affects only that env's narrower chain.
	mustPublish(t, s, l, "env e lock (narrow write in same batch is still forbidden after lock? separate publish)",
		layerconfig.Change{Op: layerconfig.OpLock, Scope: layerconfig.Scope{Env: "e"}, Key: "k"},
	)
	failPublishKind(t, s, l, layerconfig.KindLockConflict, "region under locked env rejected",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e", Region: "r2"}, Key: "k", Value: layerconfig.Value{Int: 3}},
	)
	mustPublish(t, s, l, "other env unaffected by env lock",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "other"}, Key: "k", Value: layerconfig.Value{Int: 4}},
	)
	mustPublish(t, s, l, "locking env layer can still write at its own layer",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e"}, Key: "k", Value: layerconfig.Value{Int: 5}},
	)

	// Re-locking at env requires the narrower write (env e) not to exist at a
	// narrower layer; but same-layer writes are allowed.
	mustPublish(t, s, l, "unlock env e",
		layerconfig.Change{Op: layerconfig.OpUnlock, Scope: layerconfig.Scope{Env: "e"}, Key: "k"},
	)
}

// TestLockSameBatchConflict covers lock + narrower write inside one batch,
// which must be rejected with LockConflict (the lock is effective on the
// candidate).
func TestLockSameBatchConflict(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{Key: "k", Type: layerconfig.TypeInt, Min: 0, Max: 10, Merge: layerconfig.MergeOverride}, l)
	failPublishKind(t, s, l, layerconfig.KindLockConflict,
		"global lock and narrower write in one batch rejected",
		layerconfig.Change{Op: layerconfig.OpLock, Scope: layerconfig.Scope{}, Key: "k"},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "e"}, Key: "k", Value: layerconfig.Value{Int: 1}},
	)
}
