package ontology

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		rng := rand.New(rand.NewPCG(uint64(sequence+1), uint64(2000-sequence)))
		readers := 1 + rng.IntN(5)
		quarantineAfter := 1 + rng.IntN(3)
		actual, err := NewAllocator(readers, quarantineAfter)
		if err != nil {
			t.Fatal(err)
		}
		reference := newNaiveReference(readers, quarantineAfter)
		logs := []string{
			fmt.Sprintf("sequence=%d R=%d A=%d basis=constructed allocator with alive readers and zero checkpoints", sequence, readers, quarantineAfter),
		}

		for step := 0; step < 60; step++ {
			action := rng.IntN(12)
			input := ""
			basis := ""
			var gotError, wantError error
			var gotValue, wantValue any

			switch action {
			case 0:
				batchSize := 1 + rng.IntN(4)
				splits := make([]int64, batchSize)
				for i := range splits {
					if rng.IntN(10) == 0 {
						splits[i] = int64(rng.IntN(40)) + 1_000_000_000
					} else {
						splits[i] = int64(rng.IntN(40))
					}
				}
				input = fmt.Sprintf("AddSplits(%v)", splits)
				basis = "validate range and length, then sealed and historical duplicate rejection"
				gotError = actual.AddSplits(splits)
				wantError = reference.addSplits(splits)
			case 1:
				input = "Seal()"
				basis = "idempotently close discovery"
				actual.Seal()
				reference.sealed = true
			case 2:
				cp := int64(rng.IntN(5))
				input = fmt.Sprintf("Checkpoint(%d)", cp)
				basis = "only lt+1 is accepted; current epoch is lt+1"
				gotError = actual.Checkpoint(cp)
				wantError = reference.checkpoint(cp)
			case 3:
				cp := int64(rng.IntN(5))
				input = fmt.Sprintf("Complete(%d)", cp)
				basis = "reject ahead then stale; completed checkpoints bound recovery decisions"
				gotError = actual.Complete(cp)
				wantError = reference.complete(cp)
			case 4:
				reader := rng.IntN(readers + 2)
				if rng.IntN(5) == 0 {
					reader = -1
				}
				input = fmt.Sprintf("RequestSplit(%d)", reader)
				basis = "prefer smallest local unassigned split, otherwise smallest split owned-preference bucket held by a failed reader"
				if reader >= 0 && reader < readers {
					got, err := actual.RequestSplit(reader)
					gotError = err
					gotValue = got
				} else {
					var got RequestResult
					got, gotError = actual.RequestSplit(reader)
					gotValue = got
				}
				want, err := reference.requestSplit(reader)
				wantError = err
				wantValue = want
			case 5:
				reader := rng.IntN(readers + 2)
				split := int64(rng.IntN(45))
				input = fmt.Sprintf("Finished(%d,%d)", reader, split)
				basis = "only an ASSIGNED split currently owned by an alive reader can finish"
				gotError = actual.Finished(reader, split)
				wantError = reference.finished(reader, split)
			case 6:
				reader := rng.IntN(readers + 2)
				input = fmt.Sprintf("ReaderFailed(%d)", reader)
				basis = "atomically process owned ASSIGNED/FINISHED splits in ascending split order"
				if reader >= 0 && reader < readers {
					got, err := actual.ReaderFailed(reader)
					gotError = err
					gotValue = got
				} else {
					var got ReaderFailedResult
					got, gotError = actual.ReaderFailed(reader)
					gotValue = got
				}
				want, err := reference.readerFailed(reader)
				wantError = err
				wantValue = want
			case 7:
				reader := rng.IntN(readers + 2)
				input = fmt.Sprintf("ReaderRestarted(%d)", reader)
				basis = "only a currently failed reader becomes alive and keeps retained ownership"
				gotError = actual.ReaderRestarted(reader)
				wantError = reference.readerRestarted(reader)
			case 8:
				split := int64(rng.IntN(45))
				input = fmt.Sprintf("State(%d)", split)
				basis = "observe state, owner and return count for a registered split"
				gotInfo, gotErr := actual.State(split)
				gotError = gotErr
				if record, ok := reference.records[split]; ok {
					wantError = nil
					wantValue = SplitInfo{State: record.state, Owner: record.owner, Ret: record.ret}
				} else {
					wantError = ErrInvalidArgument
				}
				if gotErr == nil {
					gotValue = gotInfo
				}
			case 9:
				input = "Quarantined()"
				basis = "return terminal quarantined split IDs in ascending order"
				gotValue = actual.Quarantined()
				ids := make([]int64, 0)
				for split, record := range reference.records {
					if record.state == Quarantined {
						ids = append(ids, split)
					}
				}
				slices.Sort(ids)
				wantValue = ids
			default:
				input = "Counts()"
				basis = "count each split in exactly one of the four lifecycle states"
				gotValue = actual.Counts()
				counts := map[SplitState]int{Unassigned: 0, Assigned: 0, Finished: 0, Quarantined: 0}
				for _, record := range reference.records {
					counts[record.state]++
				}
				wantValue = counts
			}

			logs = append(logs, fmt.Sprintf("step=%d input=%s output=got(%v,%v) want(%v,%v) basis=%s",
				step, input, gotValue, gotError, wantValue, wantError, basis))
			if !equalErrors(gotError, wantError) || !reflect.DeepEqual(normalizeValue(gotValue), normalizeValue(wantValue)) {
				t.Fatalf("sequence %d step %d mismatch\ninput=%s\ngot=(%v,%v)\nwant=(%v,%v)\nlogs:\n%s\nactual:\n%s\nreference:\n%s",
					sequence, step, input, gotValue, gotError, wantValue, wantError, joinLogs(logs),
					actual.snapshot(), reference.snapshot())
			}

			t.Logf("sequence=%d step=%d input=%s output=%v,%v basis=%s",
				sequence, step, input, normalizeValue(gotValue), gotError, basis)
		}

		if actual.snapshot() != reference.snapshot() {
			t.Fatalf("sequence %d state mismatch\nactual:\n%s\nreference:\n%s\nlogs:\n%s",
				sequence, actual.snapshot(), reference.snapshot(), joinLogs(logs))
		}
	}
}

func joinLogs(logs []string) string {
	result := ""
	for _, log := range logs {
		result += log + "\n"
	}
	return result
}

func (a *Allocator) snapshot() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ids := make([]int64, 0, len(a.records))
	for split := range a.records {
		ids = append(ids, split)
	}
	slices.Sort(ids)
	result := fmt.Sprintf("lt=%d done=%d sealed=%t alive=%v probes=%d owned=%d splits=[",
		a.latestCheckpoint, a.doneCheckpoint, a.sealed, a.alive, a.probes, a.ownedCount)
	for _, split := range ids {
		record := a.records[split]
		result += fmt.Sprintf(" %d:%s/o%d/at%d/ft%d/ret%d", split, stateName(record.state), record.owner, record.at, record.ft, record.ret)
	}
	return result + "]"
}

type naiveRecord struct {
	state SplitState
	owner int
	at    uint64
	ft    uint64
	ret   int
}

type naiveReference struct {
	readers         int
	quarantineAfter int
	records         map[int64]*naiveRecord
	alive           []bool
	latest          int64
	done            int64
	sealed          bool
	probes          int64
	ownedCount      int64
}

func newNaiveReference(readers int, quarantineAfter int) *naiveReference {
	r := &naiveReference{
		readers:         readers,
		quarantineAfter: quarantineAfter,
		records:         make(map[int64]*naiveRecord),
		alive:           make([]bool, readers),
	}
	for i := range r.alive {
		r.alive[i] = true
	}
	return r
}

func (r *naiveReference) addSplits(splits []int64) error {
	if len(splits) < 1 || len(splits) > 1000 {
		return ErrInvalidArgument
	}
	for _, split := range splits {
		if split < 0 || split > 1_000_000_000 {
			return ErrInvalidArgument
		}
	}
	if r.sealed {
		return ErrSealed
	}
	seen := make(map[int64]struct{}, len(splits))
	for _, split := range splits {
		if _, ok := r.records[split]; ok {
			return ErrDuplicateSplit
		}
		if _, ok := seen[split]; ok {
			return ErrDuplicateSplit
		}
		seen[split] = struct{}{}
	}
	for _, split := range splits {
		r.records[split] = &naiveRecord{state: Unassigned, owner: -1}
	}
	return nil
}

func (r *naiveReference) unassignedIDs() []int64 {
	ids := make([]int64, 0)
	for split, record := range r.records {
		if record.state == Unassigned {
			ids = append(ids, split)
		}
	}
	slices.Sort(ids)
	return ids
}

func (r *naiveReference) minUnassignedFor(preferred int) (int64, bool) {
	best := int64(-1)
	found := false
	for split, record := range r.records {
		if record.state == Unassigned && int(split%int64(r.readers)) == preferred {
			if !found || split < best {
				best = split
				found = true
			}
		}
	}
	return best, found
}

func (r *naiveReference) requestSplit(reader int) (RequestResult, error) {
	if reader < 0 || reader >= r.readers {
		return RequestResult{}, ErrInvalidArgument
	}
	if !r.alive[reader] {
		return RequestResult{}, ErrReaderFailed
	}

	best := int64(-1)
	bestReader := -1
	if candidate, ok := r.minUnassignedFor(reader); ok {
		best = candidate
		bestReader = reader
		r.probes++
	}
	if best == -1 {
		for preferred := 0; preferred < r.readers; preferred++ {
			if preferred == reader || r.alive[preferred] {
				continue
			}
			candidate, ok := r.minUnassignedFor(preferred)
			if !ok {
				continue
			}
			r.probes++
			if best == -1 || candidate < best {
				best = candidate
				bestReader = preferred
			}
		}
	}
	if bestReader == -1 {
		assigned := 0
		unassigned := 0
		for _, record := range r.records {
			if record.state == Assigned {
				assigned++
			}
			if record.state == Unassigned {
				unassigned++
			}
		}
		if r.sealed && unassigned == 0 && assigned == 0 {
			return RequestResult{Kind: RequestNoMoreSplits}, nil
		}
		return RequestResult{Kind: RequestWaiting}, nil
	}

	record := r.records[best]
	record.state = Assigned
	record.owner = reader
	record.at = uint64(r.latest) + 1
	return RequestResult{Kind: RequestAssigned, Split: best}, nil
}

func (r *naiveReference) finished(reader int, split int64) error {
	if reader < 0 || reader >= r.readers || split < 0 || split > 1_000_000_000 {
		return ErrInvalidArgument
	}
	if !r.alive[reader] {
		return ErrReaderFailed
	}
	record := r.records[split]
	if record == nil || record.state != Assigned || record.owner != reader {
		return ErrSplitNotAssignedToReader
	}
	record.state = Finished
	record.ft = uint64(r.latest) + 1
	return nil
}

func (r *naiveReference) readerFailed(reader int) (ReaderFailedResult, error) {
	if reader < 0 || reader >= r.readers {
		return ReaderFailedResult{}, ErrInvalidArgument
	}
	if !r.alive[reader] {
		return ReaderFailedResult{}, ErrReaderFailed
	}
	r.alive[reader] = false
	result := ReaderFailedResult{
		Returned:    []int64{},
		Quarantined: []int64{},
		Revoked:     []int64{},
	}

	ids := make([]int64, 0)
	for split, record := range r.records {
		if record.owner == reader && (record.state == Assigned || record.state == Finished) {
			ids = append(ids, split)
		}
	}
	slices.Sort(ids)
	for _, split := range ids {
		record := r.records[split]
		r.ownedCount++
		if record.at > uint64(r.done) {
			result.Returned = append(result.Returned, split)
			record.owner = -1
			record.ret++
			if record.ret >= r.quarantineAfter {
				record.state = Quarantined
				result.Quarantined = append(result.Quarantined, split)
			} else {
				record.state = Unassigned
			}
		} else if record.state == Finished && record.ft > uint64(r.done) {
			record.state = Assigned
			result.Revoked = append(result.Revoked, split)
		}
	}
	return result, nil
}

func (r *naiveReference) readerRestarted(reader int) error {
	if reader < 0 || reader >= r.readers {
		return ErrInvalidArgument
	}
	if r.alive[reader] {
		return ErrReaderAlive
	}
	r.alive[reader] = true
	return nil
}

func (r *naiveReference) checkpoint(cp int64) error {
	if cp < 1 {
		return ErrInvalidArgument
	}
	if cp != r.latest+1 {
		return ErrCheckpointOutOfOrder
	}
	r.latest = cp
	return nil
}

func (r *naiveReference) complete(cp int64) error {
	if cp < 1 {
		return ErrInvalidArgument
	}
	if cp > r.latest {
		return ErrCheckpointAhead
	}
	if cp <= r.done {
		return ErrCheckpointStale
	}
	r.done = cp
	return nil
}

func (r *naiveReference) snapshot() string {
	ids := make([]int64, 0, len(r.records))
	for split := range r.records {
		ids = append(ids, split)
	}
	slices.Sort(ids)
	result := fmt.Sprintf("lt=%d done=%d sealed=%t alive=%v probes=%d owned=%d splits=[",
		r.latest, r.done, r.sealed, r.alive, r.probes, r.ownedCount)
	for _, split := range ids {
		record := r.records[split]
		result += fmt.Sprintf(" %d:%s/o%d/at%d/ft%d/ret%d", split, stateName(record.state), record.owner, record.at, record.ft, record.ret)
	}
	return result + "]"
}

func stateName(state SplitState) string {
	switch state {
	case Unassigned:
		return "U"
	case Assigned:
		return "A"
	case Finished:
		return "F"
	case Quarantined:
		return "Q"
	default:
		return "?"
	}
}

func equalErrors(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got == want || reflect.DeepEqual(got, want)
}

func normalizeValue(value any) any {
	if ids, ok := value.([]int64); ok && ids == nil {
		return []int64{}
	}
	return value
}
