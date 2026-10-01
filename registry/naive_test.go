package registry

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

type naiveKind int

const (
	naiveLayer naiveKind = iota
	naiveManifest
	naiveIndex
)

type naiveObject struct {
	kind      naiveKind
	size      int64
	refs      []string
	deadSince int64
	hasDead   bool
	live      bool
}

type naiveModel struct {
	retention int64
	maxNow    int64
	hasClock  bool
	objects   map[string]*naiveObject
	tags      map[string]string
}

type testOp struct {
	kind   string
	digest string
	size   int64
	refs   []string
	name   string
	target string
	now    int64
}

func newNaiveModel(retention int64) *naiveModel {
	return &naiveModel{
		retention: retention,
		objects:   make(map[string]*naiveObject),
		tags:      make(map[string]string),
	}
}

func (m *naiveModel) apply(op testOp) ([]string, error) {
	if m.hasClock && op.now < m.maxNow {
		return nil, ErrClockMovedBackward
	}

	switch op.kind {
	case "layer":
		if op.digest == "" {
			return nil, ErrEmptyDigest
		}
		if op.size <= 0 {
			return nil, ErrInvalidSize
		}
		if existing, ok := m.objects[op.digest]; ok {
			if existing.kind != naiveLayer || existing.size != op.size {
				return nil, ErrDigestMismatch
			}
		} else {
			m.objects[op.digest] = &naiveObject{kind: naiveLayer, size: op.size}
			m.recompute(op.now)
		}
	case "manifest":
		refs, err := m.validateRefs(op, naiveManifest, naiveLayer)
		if err != nil {
			return nil, err
		}
		m.objects[op.digest] = &naiveObject{kind: naiveManifest, refs: refs}
		m.recompute(op.now)
	case "index":
		refs, err := m.validateRefs(op, naiveIndex, naiveManifest)
		if err != nil {
			return nil, err
		}
		m.objects[op.digest] = &naiveObject{kind: naiveIndex, refs: refs}
		m.recompute(op.now)
	case "tag":
		if op.name == "" {
			return nil, ErrEmptyTagName
		}
		target, ok := m.objects[op.target]
		if !ok {
			return nil, ErrTagTargetMissing
		}
		if target.kind == naiveLayer {
			return nil, ErrTagTargetIsLayer
		}
		m.tags[op.name] = op.target
		m.recompute(op.now)
	case "untag":
		if op.name == "" {
			return nil, ErrEmptyTagName
		}
		if _, ok := m.tags[op.name]; !ok {
			return nil, ErrTagNotFound
		}
		delete(m.tags, op.name)
		m.recompute(op.now)
	case "gc":
		deleted := make([]string, 0)
		for _, targetKind := range []naiveKind{naiveIndex, naiveManifest, naiveLayer} {
			candidates := make([]string, 0)
			for digest, obj := range m.objects {
				if obj.kind != targetKind || obj.live || !obj.hasDead || op.now-obj.deadSince < m.retention || m.hasNaiveReferrer(digest) {
					continue
				}
				candidates = append(candidates, digest)
			}
			sort.Strings(candidates)
			for _, digest := range candidates {
				delete(m.objects, digest)
				deleted = append(deleted, digest)
			}
		}
		m.accept(op.now)
		return deleted, nil
	default:
		return nil, fmt.Errorf("unknown test operation %q", op.kind)
	}

	m.accept(op.now)
	return nil, nil
}

func (m *naiveModel) validateRefs(op testOp, ownerKind, referencedKind naiveKind) ([]string, error) {
	if op.digest == "" {
		return nil, ErrEmptyDigest
	}
	if _, ok := m.objects[op.digest]; ok {
		return nil, ErrDigestAlreadyExists
	}
	if len(op.refs) == 0 {
		return nil, ErrEmptyReferenceList
	}

	seen := make(map[string]bool)
	refs := append([]string(nil), op.refs...)
	for _, digest := range refs {
		if seen[digest] {
			return nil, ErrDuplicateReference
		}
		seen[digest] = true
		obj, ok := m.objects[digest]
		if !ok {
			return nil, ErrReferencedDigestMissing
		}
		if ownerKind == naiveManifest && obj.kind != referencedKind || ownerKind == naiveIndex && obj.kind != referencedKind {
			return nil, ErrInvalidReferenceKind
		}
	}
	return refs, nil
}

func (m *naiveModel) recompute(now int64) {
	live := make(map[string]bool)
	queue := make([]string, 0)
	for _, digest := range m.tags {
		if obj, ok := m.objects[digest]; ok && obj.kind != naiveLayer && !live[digest] {
			live[digest] = true
			queue = append(queue, digest)
		}
	}

	for len(queue) > 0 {
		digest := queue[0]
		queue = queue[1:]
		obj := m.objects[digest]
		if obj.kind == naiveIndex {
			for _, ref := range obj.refs {
				if !live[ref] {
					live[ref] = true
					queue = append(queue, ref)
				}
			}
		} else if obj.kind == naiveManifest {
			for _, ref := range obj.refs {
				live[ref] = true
			}
		}
	}

	for digest, obj := range m.objects {
		wasLive := obj.live
		obj.live = live[digest]
		if obj.live {
			obj.deadSince = 0
			obj.hasDead = false
		} else if wasLive || !obj.hasDead {
			obj.deadSince = now
			obj.hasDead = true
		}
	}
}

func (m *naiveModel) hasNaiveReferrer(digest string) bool {
	for _, obj := range m.objects {
		if obj.kind == naiveManifest || obj.kind == naiveIndex {
			for _, ref := range obj.refs {
				if ref == digest {
					return true
				}
			}
		}
	}
	return false
}

func (m *naiveModel) accept(now int64) {
	m.maxNow = now
	m.hasClock = true
}

func applyToReclaimer(r *Reclaimer, op testOp) ([]string, error) {
	switch op.kind {
	case "layer":
		return nil, r.PutLayer(op.digest, op.size, op.now)
	case "manifest":
		return nil, r.PutManifest(op.digest, op.refs, op.now)
	case "index":
		return nil, r.PutIndex(op.digest, op.refs, op.now)
	case "tag":
		return nil, r.Tag(op.name, op.target, op.now)
	case "untag":
		return nil, r.Untag(op.name, op.now)
	case "gc":
		return r.GC(op.now)
	default:
		return nil, fmt.Errorf("unknown test operation %q", op.kind)
	}
}

func assertSameState(t *testing.T, r *Reclaimer, m *naiveModel, op testOp) {
	t.Helper()
	if len(r.objects) != len(m.objects) {
		t.Fatalf("object count mismatch after %+v: actual=%d naive=%d", op, len(r.objects), len(m.objects))
	}

	for digest, expected := range m.objects {
		actual, ok := r.objects[digest]
		if !ok {
			t.Fatalf("actual is missing %s after %+v", digest, op)
		}
		actualRefs := actual.layers
		expectedKind := layerKind
		if expected.kind == naiveManifest {
			actualRefs = actual.layers
			expectedKind = manifestKind
		} else if expected.kind == naiveIndex {
			actualRefs = actual.manifests
			expectedKind = indexKind
		}
		if actual.kind != expectedKind || actual.size != expected.size || !reflect.DeepEqual(actualRefs, expected.refs) || actual.live != expected.live || actual.deadSinceSet != expected.hasDead || actual.deadSince != expected.deadSince {
			t.Fatalf("state mismatch for %s after %+v: actual kind=%d size=%d refs=%v live=%t dead=%t/%d; naive kind=%d size=%d refs=%v live=%t dead=%t/%d",
				digest, op, actual.kind, actual.size, actualRefs, actual.live, actual.deadSinceSet, actual.deadSince,
				expected.kind, expected.size, expected.refs, expected.live, expected.hasDead, expected.deadSince)
		}
	}

	if !reflect.DeepEqual(r.tags, m.tags) {
		t.Fatalf("tags mismatch after %+v: actual=%v naive=%v", op, r.tags, m.tags)
	}
	if r.maxNow != m.maxNow || r.maxNowSet != m.hasClock {
		t.Fatalf("clock mismatch after %+v: actual=%d/%t naive=%d/%t", op, r.maxNow, r.maxNowSet, m.maxNow, m.hasClock)
	}
}
