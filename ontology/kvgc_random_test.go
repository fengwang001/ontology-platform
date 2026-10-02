package ontology

import (
	"bytes"
	"math/rand"
	"sort"
	"testing"
)

type oracleRecord struct {
	typ   RecordType
	ts    int64
	value []byte
}

type oracleKVGC struct {
	limit          int
	safePoint      int64
	records        map[string][]oracleRecord
	snapshots      map[int64]int64
	nextSnapshotID int64
	cursor         string
	hasCursor      bool
	accessCounts   map[string]int64
}

func newOracleKVGC(limit int) *oracleKVGC {
	return &oracleKVGC{
		limit:        limit,
		records:      make(map[string][]oracleRecord),
		snapshots:    make(map[int64]int64),
		accessCounts: make(map[string]int64),
	}
}

func (o *oracleKVGC) total() int {
	total := 0
	for _, records := range o.records {
		total += len(records)
	}
	return total
}

func (o *oracleKVGC) write(key []byte, typ RecordType, ts int64, value []byte) error {
	if len(key) == 0 || !validRecordType(typ) || ts < 1 || ts > maxTimestamp ||
		(typ != TypePut && len(value) > 0) {
		return ErrInvalidArgument
	}
	if ts <= o.safePoint {
		return ErrExpired
	}
	keyString := string(key)
	for _, current := range o.records[keyString] {
		if current.ts == ts {
			return ErrDuplicate
		}
	}
	if o.total() >= o.limit {
		return ErrFull
	}
	o.records[keyString] = append(o.records[keyString], oracleRecord{
		typ:   typ,
		ts:    ts,
		value: append([]byte(nil), value...),
	})
	sort.Slice(o.records[keyString], func(i, j int) bool {
		return o.records[keyString][i].ts < o.records[keyString][j].ts
	})
	return nil
}

func (o *oracleKVGC) get(key []byte, ts int64) ([]byte, bool, error) {
	if len(key) == 0 || ts < 1 || ts > maxTimestamp {
		return nil, false, ErrInvalidArgument
	}
	if ts < o.safePoint {
		return nil, false, ErrExpired
	}
	for index := len(o.records[string(key)]) - 1; index >= 0; index-- {
		current := o.records[string(key)][index]
		if current.ts > ts || (current.typ != TypePut && current.typ != TypeDelete) {
			continue
		}
		if current.typ == TypePut {
			return append([]byte(nil), current.value...), true, nil
		}
		return nil, false, nil
	}
	return nil, false, nil
}

func (o *oracleKVGC) openSnapshot(ts int64) (int64, error) {
	if ts < 1 || ts > maxTimestamp {
		return 0, ErrInvalidArgument
	}
	if ts < o.safePoint {
		return 0, ErrExpired
	}
	o.nextSnapshotID++
	o.snapshots[o.nextSnapshotID] = ts
	return o.nextSnapshotID, nil
}

func (o *oracleKVGC) closeSnapshot(id int64) error {
	if _, ok := o.snapshots[id]; !ok {
		return ErrSnapshotNotFound
	}
	delete(o.snapshots, id)
	return nil
}

func (o *oracleKVGC) setSafePoint(sp int64) error {
	if sp < 0 || sp > maxTimestamp {
		return ErrInvalidArgument
	}
	if sp < o.safePoint {
		return ErrRolledBack
	}
	if len(o.snapshots) > 0 {
		minimum := int64(maxTimestamp)
		for _, snapshotTS := range o.snapshots {
			if snapshotTS < minimum {
				minimum = snapshotTS
			}
		}
		if sp > minimum {
			return ErrSnapshotBlocked
		}
	}
	o.safePoint = sp
	o.cursor = ""
	o.hasCursor = false
	return nil
}

func (o *oracleKVGC) gcStep(n int) (int, error) {
	if n < 1 {
		return 0, ErrInvalidArgument
	}
	var keys []string
	for key := range o.records {
		if !o.hasCursor || key > o.cursor {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > n {
		keys = keys[:n]
	}
	for _, key := range keys {
		o.processKey(key)
	}
	if len(keys) > 0 {
		o.cursor = keys[len(keys)-1]
		o.hasCursor = true
	}
	return len(keys), nil
}

func (o *oracleKVGC) processKey(key string) {
	original := o.records[key]
	visibleCount := 0
	for _, current := range original {
		if current.ts <= o.safePoint {
			visibleCount++
		}
	}
	o.accessCounts[key] += int64(visibleCount)

	var boundary *oracleRecord
	for index := range original {
		current := &original[index]
		if current.ts <= o.safePoint && (current.typ == TypePut || current.typ == TypeDelete) {
			if boundary == nil || current.ts > boundary.ts {
				boundary = current
			}
		}
	}

	remaining := make([]oracleRecord, 0, len(original))
	for _, current := range original {
		if current.ts <= o.safePoint {
			if current.typ == TypeLock || current.typ == TypeRollback {
				continue
			}
			if boundary != nil && current.ts < boundary.ts {
				continue
			}
			if boundary != nil && current.ts == boundary.ts && boundary.typ == TypeDelete {
				continue
			}
		}
		remaining = append(remaining, oracleRecord{
			typ:   current.typ,
			ts:    current.ts,
			value: append([]byte(nil), current.value...),
		})
	}
	if len(remaining) == 0 {
		delete(o.records, key)
	} else {
		o.records[key] = remaining
	}
}

type readPoint struct {
	key   string
	ts    int64
	value []byte
	found bool
}

func TestRandomNaiveComparison(t *testing.T) {
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		seed := int64(12041002 + trial)
		rng := rand.New(rand.NewSource(seed))
		limit := 1 + rng.Intn(40)
		actual := NewKVGC(limit)
		replay := NewKVGC(limit)
		model := newOracleKVGC(limit)
		t.Logf("trial=%d seed=%d R=%d input=<new-operation-sequence>", trial, seed, limit)

		for operation := 0; operation < 90; operation++ {
			switch rng.Intn(11) {
			case 0, 1, 2, 3:
				key := randomKey(rng)
				typ := []RecordType{TypePut, TypeDelete, TypeLock, TypeRollback}[rng.Intn(4)]
				ts := randomTimestamp(rng)
				var value []byte
				if typ == TypePut {
					value = randomValue(rng)
				}
				expected := model.write(key, typ, ts, value)
				gotActual := actual.Write(key, typ, ts, value)
				gotReplay := replay.Write(key, typ, ts, value)
				t.Logf("op=%d input=Write(key=%q,type=%c,ts=%d,value=%q) output=%v decision=validate-then-insert", operation, key, typ, ts, value, gotActual)
				if gotActual != expected || gotReplay != expected {
					t.Fatalf("Write mismatch: model=%v actual=%v replay=%v", expected, gotActual, gotReplay)
				}
			case 4, 5:
				key := randomKey(rng)
				ts := randomTimestamp(rng)
				expectedValue, expectedFound, expectedErr := model.get(key, ts)
				actualValue, actualFound, actualErr := actual.Get(key, ts)
				replayValue, replayFound, replayErr := replay.Get(key, ts)
				t.Logf("op=%d input=Get(key=%q,ts=%d) output=(value=%q,found=%v,err=%v) decision=skip-L-R-and-find-latest", operation, key, ts, actualValue, actualFound, actualErr)
				if !sameRead(expectedValue, expectedFound, expectedErr, actualValue, actualFound, actualErr) ||
					!sameRead(expectedValue, expectedFound, expectedErr, replayValue, replayFound, replayErr) {
					t.Fatalf("Get mismatch at %q,%d", key, ts)
				}
			case 6:
				ts := randomTimestamp(rng)
				expectedID, expectedErr := model.openSnapshot(ts)
				actualID, actualErr := actual.OpenSnapshot(ts)
				replayID, replayErr := replay.OpenSnapshot(ts)
				t.Logf("op=%d input=OpenSnapshot(ts=%d) output=(id=%d,err=%v) decision=allow-ts>=safe-point", operation, ts, actualID, actualErr)
				if actualID != expectedID || replayID != expectedID || actualErr != expectedErr || replayErr != expectedErr {
					t.Fatalf("OpenSnapshot mismatch")
				}
			case 7:
				id := int64(1 + rng.Intn(8))
				expectedErr := model.closeSnapshot(id)
				actualErr := actual.CloseSnapshot(id)
				replayErr := replay.CloseSnapshot(id)
				t.Logf("op=%d input=CloseSnapshot(id=%d) output=%v decision=require-existing-id", operation, id, actualErr)
				if actualErr != expectedErr || replayErr != expectedErr {
					t.Fatalf("CloseSnapshot mismatch")
				}
			case 8:
				sp := randomSafePoint(rng)
				expectedErr := model.setSafePoint(sp)
				actualErr := actual.SetSafePoint(sp)
				replayErr := replay.SetSafePoint(sp)
				t.Logf("op=%d input=SetSafePoint(sp=%d) output=%v decision=monotonic-and-snapshot-minimum", operation, sp, actualErr)
				if actualErr != expectedErr || replayErr != expectedErr {
					t.Fatalf("SetSafePoint mismatch")
				}
			default:
				n := rng.Intn(6)
				before := allReadPoints(model)
				expectedCount, expectedErr := model.gcStep(n)
				actualCount, actualErr := actual.GCStep(n)
				replayCount, replayErr := replay.GCStep(n)
				t.Logf("op=%d input=GCStep(n=%d) output=(processed=%d,err=%v) decision=ordered-keys-after-cursor; checking=%d-reads", operation, n, actualCount, actualErr, len(before))
				if actualCount != expectedCount || replayCount != expectedCount ||
					actualErr != expectedErr || replayErr != expectedErr {
					t.Fatalf("GCStep mismatch")
				}
				for _, point := range before {
					value, found, err := actual.Get([]byte(point.key), point.ts)
					if err != nil || found != point.found || !bytes.Equal(value, point.value) {
						t.Fatalf("read changed by GC key=%q ts=%d before=(%q,%v) after=(%q,%v,%v)",
							point.key, point.ts, point.value, point.found, value, found, err)
					}
				}
			}
			compareStates(t, model, actual, replay)
		}
	}
}

func randomKey(rng *rand.Rand) []byte {
	if rng.Intn(5) == 0 {
		return []byte("k")
	}
	key := make([]byte, 1+rng.Intn(4))
	for index := range key {
		key[index] = byte('a' + rng.Intn(5))
	}
	return key
}

func randomTimestamp(rng *rand.Rand) int64 {
	if rng.Intn(20) == 0 {
		return int64(rng.Intn(20))
	}
	return int64(1 + rng.Intn(40))
}

func randomSafePoint(rng *rand.Rand) int64 {
	return int64(rng.Intn(42))
}

func randomValue(rng *rand.Rand) []byte {
	if rng.Intn(4) == 0 {
		return []byte{}
	}
	return []byte{byte('a' + rng.Intn(8))}
}

func sameRead(expectedValue []byte, expectedFound bool, expectedErr error,
	actualValue []byte, actualFound bool, actualErr error) bool {
	if expectedErr != actualErr || expectedFound != actualFound {
		return false
	}
	return bytes.Equal(expectedValue, actualValue)
}

func allReadPoints(model *oracleKVGC) []readPoint {
	var points []readPoint
	for key, records := range model.records {
		times := map[int64]struct{}{model.safePoint: {}, model.safePoint + 1: {}}
		for _, current := range records {
			if current.ts >= model.safePoint {
				times[current.ts] = struct{}{}
			}
		}
		for ts := range times {
			if ts < 1 || ts > maxTimestamp {
				continue
			}
			value, found, err := model.get([]byte(key), ts)
			if err != nil {
				continue
			}
			points = append(points, readPoint{key: key, ts: ts, value: append([]byte(nil), value...), found: found})
		}
	}
	return points
}

func compareStates(t *testing.T, model *oracleKVGC, actual, replay *KVGC) {
	t.Helper()
	if actual.safePoint != model.safePoint || replay.safePoint != model.safePoint ||
		actual.total != model.total() || replay.total != model.total() {
		t.Fatalf("scalar state mismatch: model(sp=%d,total=%d) actual(sp=%d,total=%d) replay(sp=%d,total=%d)",
			model.safePoint, model.total(), actual.safePoint, actual.total, replay.safePoint, replay.total)
	}
	if actual.hasCursor != model.hasCursor || replay.hasCursor != model.hasCursor ||
		string(actual.cursor) != model.cursor || string(replay.cursor) != model.cursor {
		t.Fatalf("cursor mismatch model=(%q,%v) actual=(%q,%v) replay=(%q,%v)",
			model.cursor, model.hasCursor, actual.cursor, actual.hasCursor, replay.cursor, replay.hasCursor)
	}
	for key, modelRecords := range model.records {
		actualState := actual.keys[key]
		replayState := replay.keys[key]
		if actualState == nil || replayState == nil ||
			len(actualState.records) != len(modelRecords) ||
			len(replayState.records) != len(modelRecords) {
			t.Fatalf("record mismatch for key %q", key)
		}
		for index, modelRecord := range modelRecords {
			if actualState.records[index].typ != modelRecord.typ ||
				actualState.records[index].ts != modelRecord.ts ||
				!bytes.Equal(actualState.records[index].value, modelRecord.value) ||
				replayState.records[index].typ != modelRecord.typ ||
				replayState.records[index].ts != modelRecord.ts ||
				!bytes.Equal(replayState.records[index].value, modelRecord.value) {
				t.Fatalf("record mismatch for key=%q index=%d", key, index)
			}
		}
		if actual.accessCounts[key] != model.accessCounts[key] ||
			replay.accessCounts[key] != model.accessCounts[key] {
			t.Fatalf("access count mismatch for key=%q model=%d actual=%d replay=%d",
				key, model.accessCounts[key], actual.accessCounts[key], replay.accessCounts[key])
		}
	}
	for key := range actual.keys {
		if _, ok := model.records[key]; !ok {
			t.Fatalf("actual has unexpected key %q", key)
		}
	}
	for key := range replay.keys {
		if _, ok := model.records[key]; !ok {
			t.Fatalf("replay has unexpected key %q", key)
		}
	}
}
