package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type oracleEntry struct {
	key      string
	manifest []ReadItem
	result   string
	last     uint64
}

type oracleCache struct {
	perKey int
	global int
	tick   uint64
	items  []oracleEntry
}

type oracleResult struct {
	result   string
	found    bool
	err      error
	dump     []Entry
	length   int
	basis    string
	examined uint64
}

type op struct {
	kind     int
	key      string
	manifest []ReadItem
	result   string
	current  map[string]string
}

func newOracle(perKey, global int) *oracleCache {
	return &oracleCache{perKey: perKey, global: global}
}

func (o *oracleCache) put(key string, manifest []ReadItem, result string) oracleResult {
	if key == "" {
		return oracleResult{err: ErrEmptyKey, basis: "reject before lock: empty action key"}
	}
	if len(manifest) == 0 {
		return oracleResult{err: ErrEmptyManifest, basis: "reject: empty read manifest"}
	}
	for _, item := range manifest {
		if item.Path == "" {
			return oracleResult{err: ErrEmptyPath, basis: "reject before order check: empty path"}
		}
	}
	for i := 1; i < len(manifest); i++ {
		if manifest[i].Path <= manifest[i-1].Path {
			return oracleResult{err: ErrPathsNotStrictlyOrdered, basis: "reject: paths are not strictly increasing"}
		}
	}
	for _, item := range manifest {
		if item.Digest == "" {
			return oracleResult{err: ErrEmptyDigest, basis: "reject: empty content digest"}
		}
	}
	if result == "" {
		return oracleResult{err: ErrEmptyResult, basis: "reject: empty result digest"}
	}

	for i := range o.items {
		if o.items[i].key == key && reflect.DeepEqual(o.items[i].manifest, manifest) {
			o.tick++
			o.items[i].result = result
			o.items[i].last = o.tick
			return oracleResult{basis: "duplicate manifest: replace result and refresh last"}
		}
	}

	o.tick++
	o.items = append(o.items, oracleEntry{
		key:      key,
		manifest: cloneManifest(manifest),
		result:   result,
		last:     o.tick,
	})

	o.evictKey(key)
	o.evictGlobal()
	return oracleResult{basis: "insert then per-key eviction followed by global eviction"}
}

func (o *oracleCache) lookup(key string, current map[string]string) oracleResult {
	if key == "" {
		return oracleResult{err: ErrEmptyKey, basis: "reject before lock: empty action key"}
	}

	matchIndex := -1
	examined := uint64(0)
	for i := range o.items {
		if o.items[i].key != key {
			continue
		}
		examined++
		if modelMatches(o.items[i].manifest, current) && (matchIndex == -1 || o.items[i].last > o.items[matchIndex].last) {
			matchIndex = i
		}
	}

	if matchIndex == -1 {
		return oracleResult{found: false, examined: examined, basis: "no entry has every path with an equal digest; tick unchanged"}
	}

	o.tick++
	o.items[matchIndex].last = o.tick
	return oracleResult{
		result:   o.items[matchIndex].result,
		found:    true,
		examined: examined,
		basis:    "match all listed path/digest pairs and choose greatest last; tick refreshed",
	}
}

func (o *oracleCache) dump(key string) oracleResult {
	if key == "" {
		return oracleResult{dump: []Entry{}, basis: "empty key is a normal empty dump"}
	}

	entries := []Entry{}
	for _, item := range o.items {
		if item.key == key {
			entries = append(entries, Entry{
				Manifest: cloneManifest(item.manifest),
				Result:   item.result,
				Last:     item.last,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Last > entries[j].Last })
	return oracleResult{dump: entries, basis: "entries for one key sorted by descending last"}
}

func (o *oracleCache) length() oracleResult {
	return oracleResult{length: len(o.items), basis: "count all entries across keys"}
}

func (o *oracleCache) evictKey(key string) {
	for o.countKey(key) > o.perKey {
		oldest := -1
		for i := range o.items {
			if o.items[i].key == key && (oldest == -1 || o.items[i].last < o.items[oldest].last) {
				oldest = i
			}
		}
		o.items = append(o.items[:oldest], o.items[oldest+1:]...)
	}
}

func (o *oracleCache) evictGlobal() {
	for len(o.items) > o.global {
		oldest := 0
		for i := 1; i < len(o.items); i++ {
			if o.items[i].last < o.items[oldest].last {
				oldest = i
			}
		}
		o.items = append(o.items[:oldest], o.items[oldest+1:]...)
	}
}

func (o *oracleCache) countKey(key string) int {
	count := 0
	for _, item := range o.items {
		if item.key == key {
			count++
		}
	}
	return count
}

func modelMatches(manifest []ReadItem, current map[string]string) bool {
	for _, item := range manifest {
		digest, ok := current[item.Path]
		if !ok || digest != item.Digest {
			return false
		}
	}
	return true
}

func TestRandomLinearOracle(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		rng := rand.New(rand.NewSource(int64(105000 + sequence)))
		perKey := 1 + rng.Intn(4)
		global := 1 + rng.Intn(8)

		cache, err := New(perKey, global)
		if err != nil {
			t.Fatal(err)
		}
		model := newOracle(perKey, global)

		var log []string
		for step := 0; step < 25; step++ {
			operation := randomOp(rng)
			actual := runActual(cache, operation)
			expected := runOracle(model, operation)
			log = append(log, formatOp(sequence, step, perKey, global, operation, actual, expected))

			if !resultsEqual(actual, expected) {
				log = append(log, fmt.Sprintf(
					"judgement: result=%t found=%t error=%t dump=%t length=%t examined=%t",
					actual.result == expected.result,
					actual.found == expected.found,
					errorsIs(actual.err, expected.err),
					reflect.DeepEqual(actual.dump, expected.dump),
					actual.length == expected.length,
					actual.examined == expected.examined,
				))
				t.Fatalf("oracle mismatch\n%s", strings.Join(log, "\n"))
			}
		}

		if testing.Verbose() {
			t.Logf("sequence %d inputs, outputs and basis:\n%s", sequence, strings.Join(log, "\n"))
		}
	}
}

func randomOp(rng *rand.Rand) op {
	kind := rng.Intn(4)
	operation := op{kind: kind}

	switch kind {
	case 0:
		operation.key = randomKey(rng)
		operation.manifest = randomManifest(rng)
		operation.result = randomNonEmpty(rng)
		if rng.Intn(8) == 0 {
			injectInvalidPut(&operation, rng)
		}
	case 1:
		operation.key = randomKey(rng)
		operation.current = randomCurrent(rng)
		if rng.Intn(15) == 0 {
			operation.key = ""
		}
	case 2:
		operation.key = randomKey(rng)
		if rng.Intn(15) == 0 {
			operation.key = ""
		}
	}

	return operation
}

func runActual(cache *Cache, operation op) oracleResult {
	switch operation.kind {
	case 0:
		return oracleResult{err: cache.Put(operation.key, operation.manifest, operation.result), basis: "actual put"}
	case 1:
		result, found, err := cache.Lookup(operation.key, operation.current)
		examined := uint64(0)
		if err == nil {
			cache.mu.Lock()
			examined = cache.examined
			cache.mu.Unlock()
		}
		return oracleResult{result: result, found: found, err: err, examined: examined, basis: "actual lookup"}
	case 2:
		return oracleResult{dump: cache.Dump(operation.key), basis: "actual dump"}
	default:
		return oracleResult{length: cache.Len(), basis: "actual len"}
	}
}

func runOracle(model *oracleCache, operation op) oracleResult {
	switch operation.kind {
	case 0:
		return model.put(operation.key, operation.manifest, operation.result)
	case 1:
		return model.lookup(operation.key, operation.current)
	case 2:
		return model.dump(operation.key)
	default:
		return model.length()
	}
}

func resultsEqual(actual, expected oracleResult) bool {
	return actual.result == expected.result &&
		actual.found == expected.found &&
		errorsIs(actual.err, expected.err) &&
		reflect.DeepEqual(actual.dump, expected.dump) &&
		actual.length == expected.length &&
		actual.examined == expected.examined
}

func errorsIs(actual, expected error) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return actual == expected
}

func randomKey(rng *rand.Rand) string {
	return fmt.Sprintf("k%d", rng.Intn(5))
}

func randomNonEmpty(rng *rand.Rand) string {
	return fmt.Sprintf("v%d", rng.Intn(6))
}

func randomManifest(rng *rand.Rand) []ReadItem {
	count := 1 + rng.Intn(3)
	paths := []string{
		fmt.Sprintf("p%d", rng.Intn(3)),
		fmt.Sprintf("p%d", 3+rng.Intn(3)),
		fmt.Sprintf("p%d", 6+rng.Intn(3)),
	}
	rng.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })

	items := make([]ReadItem, count)
	for i := 0; i < count; i++ {
		items[i] = ReadItem{Path: paths[i], Digest: randomNonEmpty(rng)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}

func randomCurrent(rng *rand.Rand) map[string]string {
	current := make(map[string]string)
	for _, path := range []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"} {
		switch rng.Intn(3) {
		case 0:
			current[path] = fmt.Sprintf("v%d", rng.Intn(6))
		case 1:
			current[path] = "stale"
		}
	}
	return current
}

func injectInvalidPut(operation *op, rng *rand.Rand) {
	switch rng.Intn(6) {
	case 0:
		operation.key = ""
	case 1:
		operation.manifest = nil
	case 2:
		operation.manifest[0].Path = ""
	case 3:
		if len(operation.manifest) > 1 {
			operation.manifest[0].Path, operation.manifest[1].Path = operation.manifest[1].Path, operation.manifest[0].Path
		} else {
			operation.manifest[0].Path = "p9"
			operation.manifest = append(operation.manifest, ReadItem{Path: "p1", Digest: "x"})
			sort.Slice(operation.manifest, func(i, j int) bool { return false })
		}
	case 4:
		operation.manifest[rng.Intn(len(operation.manifest))].Digest = ""
	case 5:
		operation.result = ""
	}
}

func formatOp(sequence, step, perKey, global int, operation op, actual, expected oracleResult) string {
	return fmt.Sprintf(
		"seq=%d step=%d M=%d Cap=%d input=%s actual={%s} expected={%s} basis=%q",
		sequence, step, perKey, global, formatInput(operation), formatResult(actual), formatResult(expected), expected.basis,
	)
}

func formatInput(operation op) string {
	switch operation.kind {
	case 0:
		return fmt.Sprintf("Put(%q,%#v,%q)", operation.key, operation.manifest, operation.result)
	case 1:
		return fmt.Sprintf("Lookup(%q,%#v)", operation.key, operation.current)
	case 2:
		return fmt.Sprintf("Dump(%q)", operation.key)
	default:
		return "Len()"
	}
}

func formatResult(value oracleResult) string {
	return fmt.Sprintf("result=%q found=%t err=%v dump=%#v len=%d examined=%d", value.result, value.found, value.err, value.dump, value.length, value.examined)
}
