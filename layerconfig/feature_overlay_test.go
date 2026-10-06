package layerconfig_test

import (
	"testing"

	"ontology/layerconfig"
)

// TestFourLayerOverlay verifies broad-to-narrow overlay and partial chains.
func TestFourLayerOverlay(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{
		Key: "host", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
	}, l)

	mustPublish(t, s, l, "global value",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "host", Value: layerconfig.Value{Str: "g"}},
	)
	mustPublish(t, s, l, "env overrides global",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod"}, Key: "host", Value: layerconfig.Value{Str: "e"}},
	)
	mustPublish(t, s, l, "region overrides env",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn"}, Key: "host", Value: layerconfig.Value{Str: "r"}},
	)
	mustPublish(t, s, l, "instance overrides region",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn", Instance: "i1"}, Key: "host", Value: layerconfig.Value{Str: "i"}},
	)

	checkResolve(t, s, l, -1, layerconfig.Scope{}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "g"}}, "global-only chain sees global")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod"}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "e"}}, "env chain stops at env")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "cn"}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "r"}}, "region chain sees region")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "cn", Instance: "i1"}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "i"}}, "full chain sees instance")

	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "dev"}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "g"}}, "other env inherits global")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "us"}, "host",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "e"}}, "other region inherits env")
}

// TestCancelOverrideAndAppend verifies cancellation for override scalars and
// append lists, including narrower rewrites after cancel, and the
// unset/empty distinction.
func TestCancelOverrideAndAppend(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{Key: "name", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride}, l)
	mustRegister(t, s, layerconfig.Schema{Key: "tags", Type: layerconfig.TypeStringList, Merge: layerconfig.MergeAppend}, l)

	mustPublish(t, s, l, "seed scalar and list",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "name", Value: layerconfig.Value{Str: "g"}},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "tags", Value: layerconfig.Value{List: []string{"a", "b"}}},
	)
	mustPublish(t, s, l, "env cancels both; region rewrites after",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "prod"}, Key: "name"},
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "prod"}, Key: "tags"},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn"}, Key: "name", Value: layerconfig.Value{Str: "r"}},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn"}, Key: "tags", Value: layerconfig.Value{List: []string{"c"}}},
	)

	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod"}, "name", layerconfig.Unset,
		"cancel voids broader scalar; nothing narrower on this chain")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod"}, "tags", layerconfig.Unset,
		"cancel voids the accumulated list too")
	checkResolve(t, s, l, -1, layerconfig.Scope{}, "tags",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{List: []string{"a", "b"}}}, "global unchanged")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "cn"}, "tags",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{List: []string{"c"}}},
		"narrow write after cancel does not resurrect cancelled broad elements")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "cn"}, "name",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: "r"}}, "narrow scalar after cancel")

	mustPublish(t, s, l, "write empty list at another region",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "us"}, Key: "tags", Value: layerconfig.Value{List: []string{}}},
	)
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "us"}, "tags",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{List: []string{}}},
		"empty list is a present value, distinct from unset")

	mustPublish(t, s, l, "cancel then narrower empty-string write",
		layerconfig.Change{Op: layerconfig.OpCancel, Scope: layerconfig.Scope{Env: "prod", Region: "eu"}, Key: "name"},
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "eu", Instance: "x"}, Key: "name", Value: layerconfig.Value{Str: ""}},
	)
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "eu", Instance: "x"}, "name",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{Str: ""}}, "empty string is present, not unset")
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "eu"}, "name", layerconfig.Unset,
		"region chain stops before the instance write, so still unset")
}

// TestAppendDedupPosition verifies first-position retention broad-to-narrow.
func TestAppendDedupPosition(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{Key: "tags", Type: layerconfig.TypeStringList, Merge: layerconfig.MergeAppend}, l)
	mustPublish(t, s, l, "global a,b",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "tags", Value: layerconfig.Value{List: []string{"a", "b"}}},
	)
	mustPublish(t, s, l, "env b,c",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod"}, Key: "tags", Value: layerconfig.Value{List: []string{"b", "c"}}},
	)
	mustPublish(t, s, l, "region a,d,c",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn"}, Key: "tags", Value: layerconfig.Value{List: []string{"a", "d", "c"}}},
	)
	checkResolve(t, s, l, -1, layerconfig.Scope{Env: "prod", Region: "cn"}, "tags",
		layerconfig.Resolved{Present: true, Value: layerconfig.Value{List: []string{"a", "b", "c", "d"}}},
		"elements keep earliest broad-to-narrow position; later duplicates ignored")
}
