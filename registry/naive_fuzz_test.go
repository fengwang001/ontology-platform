package registry

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		seed := int64(trial + 1)
		rng := rand.New(rand.NewSource(seed))
		retention := int64(rng.Intn(21))
		r, err := NewReclaimer(retention)
		if err != nil {
			t.Fatalf("NewReclaimer(%d): %v", retention, err)
		}
		m := newNaiveModel(retention)
		nextID := 0

		for step := 0; step < 12; step++ {
			op := generateOp(r, rng, &nextID)
			actualDeleted, actualErr := applyToReclaimer(r, op)
			naiveDeleted, naiveErr := m.apply(op)

			if !errorMatches(actualErr, naiveErr) {
				t.Fatalf("trial=%d seed=%d step=%d op=%+v actual error=%v, naive error=%v", trial+1, seed, step, op, actualErr, naiveErr)
			}
			if !reflect.DeepEqual(actualDeleted, naiveDeleted) {
				t.Fatalf("trial=%d seed=%d step=%d op=%+v actual deleted=%v, naive deleted=%v", trial+1, seed, step, op, actualDeleted, naiveDeleted)
			}

			t.Logf("trial=%d seed=%d step=%d input=%+v; output=(deleted=%v,error=%v); basis=naive model independently recomputes reachability from all tags and rebuilds index/manifest/layer GC sets in the required order", trial+1, seed, step, op, actualDeleted, actualErr)
			assertSameState(t, r, m, op)
		}
	}
}

func errorMatches(actual, expected error) bool {
	if expected == nil {
		return actual == nil
	}
	return errors.Is(actual, expected)
}

func generateOp(r *Reclaimer, rng *rand.Rand, nextID *int) testOp {
	now := r.maxNow
	if rng.Intn(5) == 0 && r.maxNowSet {
		now -= int64(rng.Intn(3) + 1)
	} else {
		now += int64(rng.Intn(4))
	}

	choice := rng.Intn(10)
	switch {
	case choice < 2:
		return generateLayerOp(r, rng, nextID, now)
	case choice < 4:
		return generateManifestOp(r, rng, nextID, now)
	case choice < 6:
		return generateIndexOp(r, rng, nextID, now)
	case choice < 8:
		return generateTagOp(r, rng, nextID, now)
	case choice == 8:
		return generateUntagOp(r, rng, now)
	default:
		return testOp{kind: "gc", now: now}
	}
}

func generateLayerOp(r *Reclaimer, rng *rand.Rand, nextID *int, now int64) testOp {
	layers := digestsByKind(r, layerKind)
	if len(layers) > 0 && rng.Intn(3) == 0 {
		digest := layers[rng.Intn(len(layers))]
		size := r.objects[digest].size
		if rng.Intn(4) == 0 {
			size++
		}
		return testOp{kind: "layer", digest: digest, size: size, now: now}
	}

	digest := newDigest("l", nextID)
	if rng.Intn(10) == 0 {
		digest = ""
	}
	size := int64(rng.Intn(20) + 1)
	if rng.Intn(10) == 0 {
		size = int64(rng.Intn(2))
	}
	return testOp{kind: "layer", digest: digest, size: size, now: now}
}

func generateManifestOp(r *Reclaimer, rng *rand.Rand, nextID *int, now int64) testOp {
	op := testOp{kind: "manifest", now: now, digest: newDigest("m", nextID)}
	manifests := digestsByKind(r, manifestKind)
	if len(manifests) > 0 && rng.Intn(4) == 0 {
		op.digest = manifests[rng.Intn(len(manifests))]
	}
	if rng.Intn(12) == 0 {
		op.digest = ""
	}

	layers := digestsByKind(r, layerKind)
	nonLayers := append(digestsByKind(r, manifestKind), digestsByKind(r, indexKind)...)
	switch {
	case rng.Intn(8) == 0:
		op.refs = nil
	case len(layers) > 0 && rng.Intn(8) == 0:
		op.refs = []string{layers[0], layers[0]}
	case rng.Intn(8) == 0:
		op.refs = []string{"missing-layer"}
	case len(nonLayers) > 0 && rng.Intn(8) == 0:
		op.refs = []string{nonLayers[rng.Intn(len(nonLayers))]}
	case len(layers) > 0:
		rng.Shuffle(len(layers), func(i, j int) { layers[i], layers[j] = layers[j], layers[i] })
		count := rng.Intn(len(layers)) + 1
		op.refs = layers[:count]
	default:
		op.refs = []string{"missing-layer"}
	}
	return op
}

func generateIndexOp(r *Reclaimer, rng *rand.Rand, nextID *int, now int64) testOp {
	op := testOp{kind: "index", now: now, digest: newDigest("i", nextID)}
	indexes := digestsByKind(r, indexKind)
	if len(indexes) > 0 && rng.Intn(4) == 0 {
		op.digest = indexes[rng.Intn(len(indexes))]
	}
	if rng.Intn(12) == 0 {
		op.digest = ""
	}

	manifests := digestsByKind(r, manifestKind)
	nonManifests := append(digestsByKind(r, layerKind), digestsByKind(r, indexKind)...)
	switch {
	case rng.Intn(8) == 0:
		op.refs = nil
	case len(manifests) > 0 && rng.Intn(8) == 0:
		op.refs = []string{manifests[0], manifests[0]}
	case rng.Intn(8) == 0:
		op.refs = []string{"missing-manifest"}
	case len(nonManifests) > 0 && rng.Intn(8) == 0:
		op.refs = []string{nonManifests[rng.Intn(len(nonManifests))]}
	case len(manifests) > 0:
		rng.Shuffle(len(manifests), func(i, j int) { manifests[i], manifests[j] = manifests[j], manifests[i] })
		count := rng.Intn(len(manifests)) + 1
		op.refs = manifests[:count]
	default:
		op.refs = []string{"missing-manifest"}
	}
	return op
}

func generateTagOp(r *Reclaimer, rng *rand.Rand, nextID *int, now int64) testOp {
	op := testOp{kind: "tag", now: now, name: newDigest("t", nextID)}
	names := tagNames(r)
	if len(names) > 0 && rng.Intn(2) == 0 {
		op.name = names[rng.Intn(len(names))]
	}

	targets := append(digestsByKind(r, manifestKind), digestsByKind(r, indexKind)...)
	layers := digestsByKind(r, layerKind)
	switch {
	case rng.Intn(10) == 0:
		op.name = ""
	case rng.Intn(8) == 0:
		op.target = "missing-target"
	case len(layers) > 0 && rng.Intn(8) == 0:
		op.target = layers[rng.Intn(len(layers))]
	case len(targets) > 0:
		op.target = targets[rng.Intn(len(targets))]
	default:
		op.target = "missing-target"
	}
	return op
}

func generateUntagOp(r *Reclaimer, rng *rand.Rand, now int64) testOp {
	names := tagNames(r)
	switch {
	case rng.Intn(10) == 0:
		return testOp{kind: "untag", name: "", now: now}
	case len(names) > 0:
		return testOp{kind: "untag", name: names[rng.Intn(len(names))], now: now}
	default:
		return testOp{kind: "untag", name: "missing-tag", now: now}
	}
}

func digestsByKind(r *Reclaimer, wanted kind) []string {
	result := make([]string, 0)
	for digest, obj := range r.objects {
		if obj.kind == wanted {
			result = append(result, digest)
		}
	}
	return result
}

func tagNames(r *Reclaimer) []string {
	result := make([]string, 0, len(r.tags))
	for name := range r.tags {
		result = append(result, name)
	}
	return result
}

func newDigest(prefix string, nextID *int) string {
	digest := fmt.Sprintf("%s%03d", prefix, *nextID)
	*nextID++
	return digest
}
