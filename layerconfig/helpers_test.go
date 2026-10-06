package layerconfig_test

import (
	"testing"

	"ontology/layerconfig"
)

func mustRegister(t *testing.T, s *layerconfig.Store, sc layerconfig.Schema, l *opLogger) {
	t.Helper()
	err := s.RegisterKey(sc)
	l.logRegister(sc, err, "schema pattern must be accepted")
	if err != nil {
		t.Fatalf("register %s: %v", sc.Key, err)
	}
}

func mustPublish(t *testing.T, s *layerconfig.Store, l *opLogger, basis string, changes ...layerconfig.Change) int {
	t.Helper()
	v, err := s.Publish(changes)
	l.logPublish(changes, v, err, basis)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return v
}

func failPublishKind(t *testing.T, s *layerconfig.Store, l *opLogger, want layerconfig.ErrorKind, basis string, changes ...layerconfig.Change) {
	t.Helper()
	before := s.CurrentVersion()
	v, err := s.Publish(changes)
	l.logPublish(changes, v, err, basis)
	e, ok := layerconfig.AsError(err)
	if !ok || e.Kind != want {
		t.Fatalf("want error kind %s, got %v", want, err)
	}
	if after := s.CurrentVersion(); after != before {
		t.Fatalf("rejected publish changed version %d -> %d", before, after)
	}
}

func checkResolve(t *testing.T, s *layerconfig.Store, l *opLogger, version int, target layerconfig.Scope, key string, want layerconfig.Resolved, basis string) {
	t.Helper()
	got, err := s.Resolve(version, target, key)
	l.logResolve(version, target, key, got, err, basis)
	if err != nil {
		t.Fatalf("resolve %s: %v", key, err)
	}
	if !resolvesEqual(got, want) {
		t.Fatalf("resolve %s: got %s want %s", key, resolvedString(got), resolvedString(want))
	}
}

func resolvesEqual(a, b layerconfig.Resolved) bool {
	if a.Present != b.Present {
		return false
	}
	if !a.Present {
		return true
	}
	av, bv := a.Value, b.Value
	if av.Str != bv.Str || av.Int != bv.Int || av.Bool != bv.Bool {
		return false
	}
	if len(av.List) != len(bv.List) {
		return false
	}
	if len(av.List) == 0 {
		return (av.List == nil) == (bv.List == nil)
	}
	for i := range av.List {
		if av.List[i] != bv.List[i] {
			return false
		}
	}
	return true
}
