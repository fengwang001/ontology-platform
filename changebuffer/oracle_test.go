package changebuffer

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type oracleEntry struct {
	size    int64
	deleted bool
}

type oracleOperation struct {
	kind Kind
	key  string
	size int64
}

type oraclePage struct {
	inPool   bool
	entries  map[string]oracleEntry
	used     int64
	queue    []oracleOperation
	bufBytes int64
}

type naiveOracle struct {
	s        int64
	kp       int
	g        int64
	pages    map[int]*oraclePage
	global   int64
	messages []string
}

func newNaiveOracle(s int64, kp int, g int64) *naiveOracle {
	return &naiveOracle{s: s, kp: kp, g: g, pages: make(map[int]*oraclePage)}
}

func (o *naiveOracle) page(number int) *oraclePage {
	target := o.pages[number]
	if target == nil {
		target = &oraclePage{entries: make(map[string]oracleEntry)}
		o.pages[number] = target
	}
	return target
}

func oracleApply(target *oraclePage, s int64, kind Kind, key string, size int64) bool {
	switch kind {
	case Insert:
		if item, ok := target.entries[key]; ok {
			item.deleted = false
			target.entries[key] = item
			return true
		}
		if s-target.used < size {
			return false
		}
		target.entries[key] = oracleEntry{size: size}
		target.used += size
		return true
	case DeleteMark:
		if item, ok := target.entries[key]; ok {
			item.deleted = true
			target.entries[key] = item
		}
		return true
	case Purge:
		if item, ok := target.entries[key]; ok && item.deleted {
			delete(target.entries, key)
			target.used -= item.size
		}
		return true
	default:
		return false
	}
}

func oracleBucket(s, f int64) int {
	if 32*f < s {
		return 0
	}
	if 16*f < s {
		return 1
	}
	if 8*f < s {
		return 2
	}
	return 3
}

func oracleLowerBound(s int64, b int) int64 {
	switch b {
	case 1:
		return s / 32
	case 2:
		return s / 16
	case 3:
		return s / 8
	default:
		return 0
	}
}

func (o *naiveOracle) clonePage(number int) *oraclePage {
	source := o.pages[number]
	clone := &oraclePage{entries: make(map[string]oracleEntry)}
	if source != nil {
		clone.inPool = source.inPool
		clone.used = source.used
		clone.bufBytes = source.bufBytes
		for key, item := range source.entries {
			clone.entries[key] = item
		}
		clone.queue = append(clone.queue, source.queue...)
	}
	return clone
}

func (o *naiveOracle) op(number int, kind Kind, key string, size int64) error {
	target := o.page(number)
	if target.inPool {
		if !oracleApply(target, o.s, kind, key, size) {
			o.messages = append(o.messages, fmt.Sprintf("Op(%d,%s,%q,%d) => page out of space (direct)", number, kind, key, size))
			return ErrPageOutOfSpace
		}
		o.messages = append(o.messages, fmt.Sprintf("Op(%d,%s,%q,%d) => direct", number, kind, key, size))
		return nil
	}

	free := o.s - target.used
	bucketNumber := oracleBucket(o.s, free)
	localLimit := oracleLowerBound(o.s, bucketNumber)
	localOK := len(target.queue) < o.kp
	creditOK := kind != Insert || target.bufBytes+size <= localLimit && o.global+size <= o.g
	if localOK && creditOK {
		target.queue = append(target.queue, oracleOperation{kind: kind, key: key, size: size})
		if kind == Insert {
			target.bufBytes += size
			o.global += size
		}
		o.messages = append(o.messages, fmt.Sprintf("Op(%d,%s,%q,%d) => buffered F=%d bucket=%d lb=%d local=%d global=%d",
			number, kind, key, size, free, bucketNumber, localLimit, target.bufBytes, o.global))
		return nil
	}

	reason := fmt.Sprintf("queue=%d/%d", len(target.queue), o.kp)
	if kind == Insert {
		reason += fmt.Sprintf(" local=%d+%d/%d global=%d+%d/%d",
			target.bufBytes, size, localLimit, o.global, size, o.g)
	}
	pageSnapshot := o.clonePage(number)
	globalSnapshot := o.global
	for _, queued := range target.queue {
		oracleApply(target, o.s, queued.kind, queued.key, queued.size)
	}
	target.queue = nil
	target.bufBytes = 0
	if !oracleApply(target, o.s, kind, key, size) {
		o.pages[number] = pageSnapshot
		o.global = globalSnapshot
		o.messages = append(o.messages, fmt.Sprintf("Op(%d,%s,%q,%d) => page out of space (forced merge rejected: %s)",
			number, kind, key, size, reason))
		return ErrPageOutOfSpace
	}
	target.inPool = true
	o.global -= pageSnapshot.bufBytes
	o.messages = append(o.messages, fmt.Sprintf("Op(%d,%s,%q,%d) => forced merge and load (%s)", number, kind, key, size, reason))
	return nil
}

func (o *naiveOracle) load(number int) error {
	target := o.page(number)
	if target.inPool {
		o.messages = append(o.messages, fmt.Sprintf("Load(%d) => already in pool", number))
		return nil
	}
	for _, queued := range target.queue {
		oracleApply(target, o.s, queued.kind, queued.key, queued.size)
	}
	o.global -= target.bufBytes
	target.queue = nil
	target.bufBytes = 0
	target.inPool = true
	o.messages = append(o.messages, fmt.Sprintf("Load(%d) => merged and loaded", number))
	return nil
}

func (o *naiveOracle) evict(number int) error {
	target := o.page(number)
	if !target.inPool {
		o.messages = append(o.messages, fmt.Sprintf("Evict(%d) => page not in pool", number))
		return ErrPageNotInPool
	}
	target.inPool = false
	o.messages = append(o.messages, fmt.Sprintf("Evict(%d) => evicted", number))
	return nil
}

func (o *naiveOracle) view(number int) map[string]entrySpec {
	logical := &oraclePage{
		entries: make(map[string]oracleEntry),
	}
	if target := o.pages[number]; target != nil {
		logical.used = target.used
		for key, item := range target.entries {
			logical.entries[key] = item
		}
		for _, queued := range target.queue {
			oracleApply(logical, o.s, queued.kind, queued.key, queued.size)
		}
	}
	result := make(map[string]entrySpec, len(logical.entries))
	for key, item := range logical.entries {
		result[key] = entrySpec{size: item.size, deleted: item.deleted}
	}
	return result
}

func oracleSignature(entries map[string]entrySpec) string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d:%t", key, entries[key].size, entries[key].deleted))
	}
	return strings.Join(parts, ";")
}

func TestRandomSequencesAgainstNaiveOracle(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(fmt.Sprintf("sequence_%04d", sequence), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, uint64(sequence+1)))
			pageSize := int64(64 + rng.IntN(257))
			maxPerPage := 1 + rng.IntN(5)
			globalLimit := int64(1 + rng.IntN(int(pageSize*4)+20))
			cb := mustNew(t, pageSize, maxPerPage, globalLimit)
			oracle := newNaiveOracle(pageSize, maxPerPage, globalLimit)
			oracle.messages = append(oracle.messages,
				fmt.Sprintf("S=%d Kp=%d G=%d", pageSize, maxPerPage, globalLimit))

			for step := 0; step < 45; step++ {
				pageNo := rng.IntN(7)
				command := rng.IntN(10)
				switch {
				case command < 7:
					kind := []Kind{Insert, Insert, Insert, DeleteMark, DeleteMark, Purge, Purge}[command]
					key := string(rune('a' + rng.IntN(5)))
					size := int64(0)
					if kind == Insert {
						switch rng.IntN(6) {
						case 0:
							size = 1
						case 1:
							size = pageSize
						case 2:
							size = max(1, pageSize/int64(2+rng.IntN(3)))
						case 3:
							size = 1 + rng.Int64N(max(2, pageSize/8))
						default:
							size = 1 + rng.Int64N(pageSize)
						}
					}
					oracle.messages = append(oracle.messages, fmt.Sprintf("input: Op(%d,%s,%q,%d)", pageNo, kind, key, size))
					gotErr := cb.Op(pageNo, kind, []byte(key), size)
					wantErr := oracle.op(pageNo, kind, key, size)
					compareErrors(t, gotErr, wantErr, oracle.messages)
				case command == 7:
					oracle.messages = append(oracle.messages, fmt.Sprintf("input: Load(%d)", pageNo))
					gotErr := cb.Load(pageNo)
					wantErr := oracle.load(pageNo)
					compareErrors(t, gotErr, wantErr, oracle.messages)
				case command == 8:
					oracle.messages = append(oracle.messages, fmt.Sprintf("input: Evict(%d)", pageNo))
					gotErr := cb.Evict(pageNo)
					wantErr := oracle.evict(pageNo)
					compareErrors(t, gotErr, wantErr, oracle.messages)
				default:
					oracle.messages = append(oracle.messages, fmt.Sprintf("input: View(%d)", pageNo))
					gotView, gotErr := cb.View(pageNo)
					if gotErr != nil {
						t.Fatalf("View returned error: %v\n%s", gotErr, strings.Join(oracle.messages, "\n"))
					}
					wantView := oracle.view(pageNo)
					gotMap := make(map[string]entrySpec, len(gotView))
					for _, item := range gotView {
						gotMap[string(item.Key)] = entrySpec{size: item.Size, deleted: item.Deleted}
					}
					if oracleSignature(gotMap) != oracleSignature(wantView) {
						t.Fatalf("View mismatch\ngot=%v\nwant=%v\n%s",
							gotMap, wantView, strings.Join(oracle.messages, "\n"))
					}
					oracle.messages = append(oracle.messages, fmt.Sprintf("output: View(%d) = %s", pageNo, oracleSignature(wantView)))
				}
				compareOracleState(t, cb, oracle)
			}
			for _, line := range oracle.messages {
				t.Log(line)
			}
		})
	}
}

func compareErrors(t *testing.T, got, want error, messages []string) {
	t.Helper()
	if got != want {
		t.Fatalf("error mismatch got=%v want=%v\n%s", got, want, strings.Join(messages, "\n"))
	}
}

func compareOracleState(t *testing.T, cb *ChangeBuffer, oracle *naiveOracle) {
	t.Helper()
	for number := 0; number < 7; number++ {
		got := cb.pages[number]
		want := oracle.pages[number]
		if got == nil && want == nil {
			continue
		}
		if got == nil || want == nil {
			t.Fatalf("page %d presence mismatch\ngot=%+v\nwant=%+v\n%s", number, got, want, strings.Join(oracle.messages, "\n"))
		}
		if got.inPool != want.inPool || got.used != want.used || got.bufBytes != want.bufBytes ||
			len(got.entries) != len(want.entries) || len(got.queue) != len(want.queue) {
			t.Fatalf("page %d state mismatch\ngot=%+v entries=%v queue=%+v\nwant=%+v entries=%v queue=%+v\n%s",
				number, got, got.entries, got.queue, want, want.entries, want.queue, strings.Join(oracle.messages, "\n"))
		}
		for key, item := range want.entries {
			gotItem := got.entries[key]
			wantItem := entrySpec{size: item.size, deleted: item.deleted}
			if (entrySpec{size: gotItem.size, deleted: gotItem.deleted}) != wantItem {
				t.Fatalf("page %d entry %s mismatch got=%+v want=%+v\n%s", number, key, gotItem, item, strings.Join(oracle.messages, "\n"))
			}
		}
		for i := range want.queue {
			gotQueued := got.queue[i]
			wantQueued := want.queue[i]
			if gotQueued.kind != wantQueued.kind || gotQueued.key != wantQueued.key || gotQueued.size != wantQueued.size {
				t.Fatalf("page %d queue[%d] mismatch got=%+v want=%+v\n%s", number, i, gotQueued, wantQueued, strings.Join(oracle.messages, "\n"))
			}
		}
		if oracleSignature(oracle.view(number)) != oracleSignature(viewMap(t, cb, number)) {
			t.Fatalf("page %d logical view mismatch\n%s", number, strings.Join(oracle.messages, "\n"))
		}
	}
	if cb.globalBuf != oracle.global {
		t.Fatalf("global mismatch got=%d want=%d\n%s", cb.globalBuf, oracle.global, strings.Join(oracle.messages, "\n"))
	}
	assertInvariants(t, cb)
}
