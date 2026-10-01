package heap

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestVacuumVisibilityBoundaries(t *testing.T) {
	h, err := NewHeap(1, 4)
	if err != nil {
		t.Fatalf("NewHeap() error = %v", err)
	}

	older, err := h.Insert("older", 9)
	_, err = h.Insert("equal", 10)
	if err != nil {
		t.Fatalf("Insert(equal) error = %v", err)
	}
	deletedAtHorizon, err := h.Insert("deleted-eq", 5)
	if err != nil {
		t.Fatalf("Insert(deleted-eq) error = %v", err)
	}
	deletedBeforeHorizon, err := h.Insert("deleted-before", 5)
	if err != nil {
		t.Fatalf("Insert(deleted-before) error = %v", err)
	}

	if err := h.Delete(deletedAtHorizon.Page, deletedAtHorizon.Slot, 10); err != nil {
		t.Fatalf("Delete(deleted-eq) error = %v", err)
	}
	if err := h.Delete(deletedBeforeHorizon.Page, deletedBeforeHorizon.Slot, 9); err != nil {
		t.Fatalf("Delete(deleted-before) error = %v", err)
	}

	if err := h.Vacuum(0, 10); err != nil {
		t.Fatalf("Vacuum() error = %v", err)
	}

	snapshotID, err := h.Snapshot(10)
	if err != nil {
		t.Fatalf("Snapshot(10) error = %v", err)
	}
	result, err := h.Scan("a", "z", snapshotID)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got := rowsForTest(result)
	want := []Row{
		{Key: "deleted-eq", Page: deletedAtHorizon.Page, Slot: deletedAtHorizon.Slot},
		{Key: "older", Page: older.Page, Slot: older.Slot},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v; xmin=h is not globally all-visible, xmax=h is retained", got, want)
	}
	if result.HeapFetches != 3 || result.IndexOnlyReads != 0 {
		t.Fatalf("counts = fetches:%d index-only:%d, want 3 and 0", result.HeapFetches, result.IndexOnlyReads)
	}
}

func TestInsertDeleteClearVisibilityAndScanCounts(t *testing.T) {
	h, err := NewHeap(3, 1)
	if err != nil {
		t.Fatalf("NewHeap() error = %v", err)
	}

	first, err := h.Insert("a", 1)
	if err != nil {
		t.Fatalf("Insert(first) error = %v", err)
	}
	second, err := h.Insert("b", 1)
	if err != nil {
		t.Fatalf("Insert(second) error = %v", err)
	}
	if err := h.Vacuum(first.Page, 5); err != nil {
		t.Fatalf("Vacuum(first page) error = %v", err)
	}
	if err := h.Vacuum(second.Page, 5); err != nil {
		t.Fatalf("Vacuum(second page) error = %v", err)
	}

	snapshotID, err := h.Snapshot(10)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	before, err := h.Scan("a", "c", snapshotID)
	if err != nil {
		t.Fatalf("Scan(before) error = %v", err)
	}
	if before.HeapFetches != 0 || before.IndexOnlyReads != 2 {
		t.Fatalf("before counts = fetches:%d index-only:%d, want 0 and 2", before.HeapFetches, before.IndexOnlyReads)
	}

	third, err := h.Insert("c", 6)
	if err != nil {
		t.Fatalf("Insert(third) error = %v", err)
	}
	if err := h.Delete(second.Page, second.Slot, 7); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	after, err := h.Scan("a", "d", snapshotID)
	if err != nil {
		t.Fatalf("Scan(after) error = %v", err)
	}
	got := rowsForTest(after)
	want := []Row{
		{Key: "a", Page: first.Page, Slot: first.Slot},
		{Key: "c", Page: third.Page, Slot: third.Slot},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if after.HeapFetches != 2 || after.IndexOnlyReads != 1 {
		t.Fatalf("after counts = fetches:%d index-only:%d, want 2 and 1", after.HeapFetches, after.IndexOnlyReads)
	}
}

func TestEmptyPageIsAllVisibleAndVacuumSnapshotRules(t *testing.T) {
	h, err := NewHeap(2, 1)
	if err != nil {
		t.Fatalf("NewHeap() error = %v", err)
	}

	if err := h.Vacuum(1, 10); err != nil {
		t.Fatalf("Vacuum(empty page) error = %v", err)
	}
	snapshotAtHorizon, err := h.Snapshot(10)
	if err != nil {
		t.Fatalf("Snapshot(10) error = %v", err)
	}
	if err := h.Release(snapshotAtHorizon); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	olderSnapshot, err := h.Snapshot(9)
	if !errors.Is(err, ErrSnapshotTooOld) {
		t.Fatalf("Snapshot(9) error = %v, want %v", err, ErrSnapshotTooOld)
	}

	olderSnapshot, err = h.Snapshot(10)
	if err != nil {
		t.Fatalf("Snapshot(10) error = %v", err)
	}
	if err := h.Vacuum(0, 11); !errors.Is(err, ErrActiveSnapshotTooOld) {
		t.Fatalf("Vacuum with active s=h-1 error = %v, want %v", err, ErrActiveSnapshotTooOld)
	}
	if err := h.Release(olderSnapshot); err != nil {
		t.Fatalf("Release(olderSnapshot) error = %v", err)
	}
	if err := h.Vacuum(0, 11); err != nil {
		t.Fatalf("Vacuum after release error = %v", err)
	}

	result, err := h.Scan("", "z", 999)
	if !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("Scan with rejected id error = %v, want %v", err, ErrUnknownSnapshot)
	}
	if !reflect.DeepEqual(result, ScanResult{}) {
		t.Fatalf("rejected result = %+v, want zero value", result)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	h, err := NewHeap(1, 1)
	if err != nil {
		t.Fatalf("NewHeap() error = %v", err)
	}
	location, err := h.Insert("a", 1)
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	snapshotID, err := h.Snapshot(2)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid insert xid", func() error { _, err := h.Insert("b", 0); return err }, ErrInvalidTransactionID},
		{"full insert", func() error { _, err := h.Insert("b", 2); return err }, ErrNoEmptySlot},
		{"invalid delete xid", func() error { return h.Delete(location.Page, location.Slot, 0) }, ErrInvalidTransactionID},
		{"delete bad page", func() error { return h.Delete(1, 0, 2) }, ErrInvalidPage},
		{"delete bad slot", func() error { return h.Delete(0, 1, 2) }, ErrInvalidSlot},
	}

	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s error = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := h.Delete(location.Page, location.Slot, 2); err != nil {
		t.Fatalf("valid Delete() error = %v", err)
	}
	if err := h.Delete(location.Page, location.Slot, 3); !errors.Is(err, ErrAlreadyDeleted) {
		t.Fatalf("second delete error = %v, want %v", err, ErrAlreadyDeleted)
	}

	if err := h.Vacuum(0, 3); !errors.Is(err, ErrActiveSnapshotTooOld) {
		t.Fatalf("Vacuum rejected error = %v, want %v", err, ErrActiveSnapshotTooOld)
	}
	result, err := h.Scan("a", "b", snapshotID)
	if err != nil {
		t.Fatalf("Scan after rejections error = %v", err)
	}
	if len(result.Rows) != 1 || result.HeapFetches != 1 || result.IndexOnlyReads != 0 {
		t.Fatalf("state changed after rejections: %+v", result)
	}

	if err := h.Release(snapshotID); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := h.Vacuum(0, 3); err != nil {
		t.Fatalf("Vacuum() error = %v", err)
	}
	if _, err := h.Snapshot(2); !errors.Is(err, ErrSnapshotTooOld) {
		t.Fatalf("Snapshot below global horizon error = %v", err)
	}
	if err := h.Release(999); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("unknown release error = %v", err)
	}
}

func rowsForTest(result ScanResult) []Row {
	if len(result.Rows) == 0 {
		return []Row{}
	}
	return result.Rows
}

type oracleRow struct {
	key  string
	xmin int
	xmax int
	used bool
}

type oracleState struct {
	pages      [][]oracleRow
	allVisible []bool
	snapshots  map[int]int
	nextID     int
	horizon    int
}

func newOracleState(pageCount, slotCount int) *oracleState {
	pages := make([][]oracleRow, pageCount)
	for page := range pages {
		pages[page] = make([]oracleRow, slotCount)
	}
	return &oracleState{
		pages:      pages,
		allVisible: make([]bool, pageCount),
		snapshots:  make(map[int]int),
	}
}

type oracleScanResult struct {
	rows           []Row
	heapFetches    int
	indexOnlyReads int
}

func TestRandomSequencesMatchAlwaysFetchOracle(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(421078))

	for sequence := 1; sequence <= sequences; sequence++ {
		pageCount := 1 + rng.Intn(4)
		slotCount := 1 + rng.Intn(4)
		table, err := NewHeap(pageCount, slotCount)
		if err != nil {
			t.Fatalf("sequence %d: NewHeap error = %v", sequence, err)
		}
		oracle := newOracleState(pageCount, slotCount)
		log := []string{fmt.Sprintf("seq=%d NewHeap(P=%d,C=%d)", sequence, pageCount, slotCount)}

		for step := 0; step < 2+rng.Intn(34); step++ {
			op := rng.Intn(100)
			switch {
			case op < 32:
				key := fmt.Sprintf("k%02d", rng.Intn(7))
				xid := 1 + rng.Intn(12)
				log = append(log, fmt.Sprintf("step=%d op=Insert input={key:%q,xid:%d}", step, key, xid))
				got, gotErr := table.Insert(key, xid)
				want, wantErr := oracleInsert(oracle, key, xid)
				reason := fmt.Sprintf("insert uses lowest empty page/slot and clears that page visibility; got=(%d,%d) err=%v want=(%d,%d) err=%v", got.Page, got.Slot, gotErr, want.Page, want.Slot, wantErr)
				t.Logf("%s output={page:%d,slot:%d,error:%v} basis=%s", log[len(log)-1], got.Page, got.Slot, gotErr, reason)
				if !sameError(gotErr, wantErr) || (gotErr == nil && got != want) {
					t.Fatalf("sequence %d step %d: Insert mismatch: got=(%v,%v), want=(%v,%v); log=%v", sequence, step, got, gotErr, want, wantErr, log)
				}
			case op < 54:
				page := rng.Intn(pageCount + 1)
				slot := rng.Intn(slotCount + 1)
				xid := 1 + rng.Intn(12)
				log = append(log, fmt.Sprintf("step=%d op=Delete input={page:%d,slot:%d,xid:%d}", step, page, slot, xid))
				gotErr := table.Delete(page, slot, xid)
				wantErr := oracleDelete(oracle, page, slot, xid)
				reason := "valid delete writes xmax and clears page visibility; invalid delete changes no state"
				t.Logf("%s output={error:%v} basis=%s", log[len(log)-1], gotErr, reason)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("sequence %d step %d: Delete mismatch: got=%v, want=%v; log=%v", sequence, step, gotErr, wantErr, log)
				}
			case op < 68:
				s := 1 + rng.Intn(14)
				log = append(log, fmt.Sprintf("step=%d op=Snapshot input={s:%d}", step, s))
				gotID, gotErr := table.Snapshot(s)
				wantID, wantErr := oracleSnapshot(oracle, s)
				reason := fmt.Sprintf("snapshot is accepted only when s>=global horizon %d and rejected calls do not consume an id", oracle.horizon)
				t.Logf("%s output={id:%d,error:%v} basis=%s", log[len(log)-1], gotID, gotErr, reason)
				if !sameError(gotErr, wantErr) || gotID != wantID {
					t.Fatalf("sequence %d step %d: Snapshot mismatch: got=(%d,%v), want=(%d,%v); log=%v", sequence, step, gotID, gotErr, wantID, wantErr, log)
				}
			case op < 78:
				id := pickSnapshotID(rng, oracle)
				log = append(log, fmt.Sprintf("step=%d op=Release input={id:%d}", step, id))
				gotErr := table.Release(id)
				wantErr := oracleRelease(oracle, id)
				reason := "only a currently registered snapshot id can be released"
				t.Logf("%s output={error:%v} basis=%s", log[len(log)-1], gotErr, reason)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("sequence %d step %d: Release mismatch: got=%v, want=%v; log=%v", sequence, step, gotErr, wantErr, log)
				}
			case op < 92:
				page := rng.Intn(pageCount + 1)
				horizon := 1 + rng.Intn(14)
				log = append(log, fmt.Sprintf("step=%d op=Vacuum input={page:%d,h:%d}", step, page, horizon))
				gotErr := table.Vacuum(page, horizon)
				wantErr := oracleVacuum(oracle, page, horizon)
				reason := "remove exactly xmax>0 and xmax<h; set bit exactly when every remaining row has xmin<h and xmax=0"
				t.Logf("%s output={error:%v} basis=%s", log[len(log)-1], gotErr, reason)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("sequence %d step %d: Vacuum mismatch: got=%v, want=%v; log=%v", sequence, step, gotErr, wantErr, log)
				}
			default:
				if len(oracle.snapshots) == 0 {
					step--
					continue
				}
				id := pickSnapshotID(rng, oracle)
				lo := fmt.Sprintf("k%02d", rng.Intn(5))
				hi := lo
				if rng.Intn(2) == 0 {
					hi = fmt.Sprintf("k%02d", rng.Intn(8))
				}
				log = append(log, fmt.Sprintf("step=%d op=Scan input={lo:%q,hi:%q,snapshot:%d}", step, lo, hi, id))
				got, gotErr := table.Scan(lo, hi, id)
				want, wantErr := oracleScan(oracle, lo, hi, id)
				reason := fmt.Sprintf("ordered index entries use all-visible pages without fetch; non-visible-map entries fetched=%d and independently checked against xmin/xmax", want.heapFetches)
				t.Logf("%s output={rows:%v,fetches:%d,indexOnly:%d,error:%v} basis=%s", log[len(log)-1], got.Rows, got.HeapFetches, got.IndexOnlyReads, gotErr, reason)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("sequence %d step %d: Scan error mismatch: got=%v, want=%v; log=%v", sequence, step, gotErr, wantErr, log)
				}
				if gotErr == nil && !reflect.DeepEqual(got.Rows, want.rows) {
					t.Fatalf("sequence %d step %d: rows mismatch:\n got=%v\nwant=%v\nlog=%v", sequence, step, got.Rows, want.rows, log)
				}
				if gotErr == nil && (got.HeapFetches != want.heapFetches || got.IndexOnlyReads != want.indexOnlyReads) {
					t.Fatalf("sequence %d step %d: counts mismatch: got=(%d,%d), want=(%d,%d); log=%v", sequence, step, got.HeapFetches, got.IndexOnlyReads, want.heapFetches, want.indexOnlyReads, log)
				}
			}
		}
	}
}

func oracleInsert(state *oracleState, key string, xid int) (Location, error) {
	if xid <= 0 {
		return Location{}, ErrInvalidTransactionID
	}
	for page := range state.pages {
		for slot, row := range state.pages[page] {
			if !row.used {
				state.pages[page][slot] = oracleRow{used: true, key: key, xmin: xid}
				state.allVisible[page] = false
				return Location{Page: page, Slot: slot}, nil
			}
		}
	}
	return Location{}, ErrNoEmptySlot
}

func oracleDelete(state *oracleState, page, slot, xid int) error {
	if xid <= 0 {
		return ErrInvalidTransactionID
	}
	if page < 0 || page >= len(state.pages) {
		return ErrInvalidPage
	}
	if slot < 0 || slot >= len(state.pages[page]) {
		return ErrInvalidSlot
	}
	if !state.pages[page][slot].used {
		return ErrEmptySlot
	}
	if state.pages[page][slot].xmax != 0 {
		return ErrAlreadyDeleted
	}
	state.pages[page][slot].xmax = xid
	state.allVisible[page] = false
	return nil
}

func oracleSnapshot(state *oracleState, s int) (int, error) {
	if s <= 0 {
		return 0, ErrInvalidSnapshotValue
	}
	if s < state.horizon {
		return 0, ErrSnapshotTooOld
	}
	state.nextID++
	state.snapshots[state.nextID] = s
	return state.nextID, nil
}

func oracleRelease(state *oracleState, id int) error {
	if _, ok := state.snapshots[id]; !ok {
		return ErrUnknownSnapshot
	}
	delete(state.snapshots, id)
	return nil
}

func oracleVacuum(state *oracleState, page, horizon int) error {
	if horizon <= 0 {
		return ErrInvalidVacuumHorizon
	}
	if page < 0 || page >= len(state.pages) {
		return ErrInvalidPage
	}
	for _, s := range state.snapshots {
		if s < horizon {
			return ErrActiveSnapshotTooOld
		}
	}
	for slot := range state.pages[page] {
		row := state.pages[page][slot]
		if row.used && row.xmax != 0 && row.xmax < horizon {
			state.pages[page][slot] = oracleRow{}
		}
	}
	visible := true
	for _, row := range state.pages[page] {
		if row.used && (row.xmin >= horizon || row.xmax != 0) {
			visible = false
		}
	}
	state.allVisible[page] = visible
	if horizon > state.horizon {
		state.horizon = horizon
	}
	return nil
}

func oracleScan(state *oracleState, lo, hi string, id int) (oracleScanResult, error) {
	if lo > hi {
		return oracleScanResult{}, ErrInvalidKeyRange
	}
	snapshot, ok := state.snapshots[id]
	if !ok {
		return oracleScanResult{}, ErrUnknownSnapshot
	}

	type indexedRow struct {
		row  Row
		page int
	}
	var entries []indexedRow
	for page := range state.pages {
		for slot, row := range state.pages[page] {
			if row.used && lo <= row.key && row.key < hi {
				entries = append(entries, indexedRow{
					row:  Row{Key: row.key, Page: page, Slot: slot},
					page: page,
				})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].row
		right := entries[j].row
		return left.Key < right.Key || (left.Key == right.Key && (left.Page < right.Page || (left.Page == right.Page && left.Slot < right.Slot)))
	})

	result := oracleScanResult{rows: []Row{}}
	for _, entry := range entries {
		if state.allVisible[entry.page] {
			result.indexOnlyReads++
			result.rows = append(result.rows, entry.row)
			continue
		}
		result.heapFetches++
		row := state.pages[entry.page][entry.row.Slot]
		if row.xmin < snapshot && (row.xmax == 0 || row.xmax >= snapshot) {
			result.rows = append(result.rows, entry.row)
		}
	}
	return result, nil
}

func pickSnapshotID(rng *rand.Rand, state *oracleState) int {
	ids := make([]int, 0, len(state.snapshots)+1)
	for id := range state.snapshots {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return rng.Intn(4) + 1
	}
	if rng.Intn(5) == 0 {
		missing := 1
		for {
			if _, ok := state.snapshots[missing]; !ok {
				return missing
			}
			missing++
		}
	}
	return ids[rng.Intn(len(ids))]
}

func sameError(got, want error) bool {
	return errors.Is(got, want) && errors.Is(want, got)
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	table, err := NewHeap(6, 8)
	if err != nil {
		t.Fatalf("NewHeap() error = %v", err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			snapshotIDs := make(chan int, 32)
			for step := 0; step < 100; step++ {
				switch rng.Intn(7) {
				case 0:
					_, _ = table.Insert(fmt.Sprintf("k%d", rng.Intn(8)), 1+rng.Intn(20))
				case 1:
					_ = table.Delete(rng.Intn(6), rng.Intn(8), 1+rng.Intn(20))
				case 2:
					if id, err := table.Snapshot(1 + rng.Intn(20)); err == nil {
						select {
						case snapshotIDs <- id:
						default:
						}
					}
				case 3:
					select {
					case id := <-snapshotIDs:
						_ = table.Release(id)
					default:
					}
				case 4:
					_ = table.Vacuum(rng.Intn(7), 1+rng.Intn(20))
				case 5:
					select {
					case id := <-snapshotIDs:
						_, _ = table.Scan("k0", "k9", id)
						select {
						case snapshotIDs <- id:
						default:
							_ = table.Release(id)
						}
					default:
					}
				default:
					_, _ = table.Snapshot(1 + rng.Intn(20))
				}
			}
			for {
				select {
				case id := <-snapshotIDs:
					_ = table.Release(id)
				default:
					return
				}
			}
		}(int64(worker + 1))
	}
	wg.Wait()
}
