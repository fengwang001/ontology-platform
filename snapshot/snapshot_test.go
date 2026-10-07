package snapshot

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func object(id string) Record {
	return Record{Kind: KindObject, Operation: OpUpsert, ID: id}
}

func action(id string) Record {
	return Record{Kind: KindAction, Operation: OpUpsert, ID: id}
}

func link(id, source, target string) Record {
	return Record{Kind: KindLink, Operation: OpUpsert, ID: id, SourceID: source, TargetID: target}
}

func transaction(t *testing.T, events []Event, txID string, records ...Record) []Event {
	t.Helper()
	for _, record := range records {
		copyRecord := record
		events = append(events, Event{Type: EventPrepare, TxID: txID, Record: &copyRecord})
	}
	next := int64(1)
	for _, event := range events {
		if event.Type == EventCommit {
			next = event.CommitLSN + 1
		}
	}
	events = append(events, Event{Type: EventCommit, TxID: txID, CommitLSN: next})
	return events
}

func TestBoundaryTieGoesEntirelyToSnapshot(t *testing.T) {
	var events []Event
	events = transaction(t, events, "tx1", object("o1"))
	events = transaction(t, events, "tx2", object("o2"))

	frames, err := NewExporter(events).Export(1, NoLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("expected snapshot then one delta then done, got %d frames", len(frames))
	}
	if frames[0].Snapshot == nil || frames[0].Snapshot.Boundary != 1 {
		t.Fatalf("first frame is not boundary 1: %#v", frames[0])
	}
	if _, ok := frames[0].Snapshot.Objects["o1"]; !ok {
		t.Fatal("snapshot omitted tied commit tx1")
	}
	if _, ok := frames[0].Snapshot.Objects["o2"]; ok {
		t.Fatal("snapshot included commit after boundary")
	}
	if len(frames[1].Deltas) != 1 || frames[1].Deltas[0].CommitLSN != 2 || frames[1].Deltas[0].TxID != "tx2" {
		t.Fatalf("unexpected delta frame: %#v", frames[1])
	}
}

func TestCrossingBoundaryTransactionIsAtomic(t *testing.T) {
	var events []Event
	events = append(events, Event{Type: EventPrepare, TxID: "prepare-first", Record: recordPtr(object("early"))})
	events = transaction(t, events, "prepare-first")
	events = append(events, Event{Type: EventPrepare, TxID: "straddle", Record: recordPtr(object("straddle-object"))})
	events = append(events, Event{Type: EventPrepare, TxID: "other", Record: recordPtr(action("action"))})
	events = append(events, Event{Type: EventCommit, TxID: "other", CommitLSN: 2})
	events = append(events, Event{Type: EventCommit, TxID: "straddle", CommitLSN: 3})

	frames, err := NewExporter(events).Export(2, NoLimits)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := frames[0].Snapshot
	if _, ok := snapshot.Objects["early"]; !ok {
		t.Fatal("snapshot missing first transaction")
	}
	if _, ok := snapshot.Objects["straddle-object"]; ok {
		t.Fatal("prepared record became visible before its commit")
	}
	if _, ok := snapshot.Actions["action"]; !ok {
		t.Fatal("snapshot missing complete boundary transaction")
	}
	var found bool
	for _, frame := range frames[1:] {
		for _, delta := range frame.Deltas {
			if delta.TxID == "straddle" {
				found = true
				if len(delta.Records) != 1 || delta.Records[0].ID != "straddle-object" {
					t.Fatalf("straddle transaction was split: %#v", delta)
				}
			}
		}
	}
	if !found {
		t.Fatal("straddle transaction not emitted as one post-boundary delta")
	}
}

func TestReferentialIntegrityAcrossInterleavings(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		name := fmt.Sprintf("order-%d-%d", order[0], order[1])
		t.Run(name, func(t *testing.T) {
			records := []Record{object("o1"), link("l1", "o1", "o1")}
			var events []Event
			events = transaction(t, events, "atomic", records[order[0]], records[order[1]])
			frames, err := NewExporter(events).Export(AutoBoundary, NoLimits)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := frames[0].Snapshot
			if _, ok := snapshot.Objects["o1"]; !ok || len(snapshot.Links) != 1 {
				t.Fatalf("atomic transaction exposed incomplete endpoint state: %#v", snapshot)
			}
		})
	}

	var bad []Event
	bad = transaction(t, bad, "link-only", link("l1", "missing", "missing"))
	_, err := NewExporter(bad).Export(AutoBoundary, NoLimits)
	if exportErr, ok := err.(*ExportError); !ok || exportErr.Class != ClassIntegrity {
		t.Fatalf("expected integrity error, got %v", err)
	}
}

func TestErrorPriorityBoundaryAtomicityIntegrityResource(t *testing.T) {
	tests := []struct {
		name     string
		boundary int64
		events   []Event
		class    ErrorClass
	}{
		{name: "boundary", boundary: 9, events: nil, class: ClassBoundary},
		{name: "atomicity", boundary: AutoBoundary, events: []Event{{
			Type: EventCommit, TxID: "missing", CommitLSN: 1,
		}}, class: ClassAtomicity},
		{name: "integrity", boundary: AutoBoundary, events: transaction(t, nil, "bad-link", link("l", "x", "x")), class: ClassIntegrity},
		{name: "resource", boundary: AutoBoundary, events: transaction(t, nil, "tx", object("o")), class: ClassResource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := NoLimits
			if tt.class == ClassResource {
				limits.MaxRecords = 0
			}
			_, err := NewExporter(tt.events).Export(tt.boundary, limits)
			if exportErr, ok := err.(*ExportError); !ok || exportErr.Class != tt.class {
				t.Fatalf("expected %s, got %v", tt.class, err)
			}
		})
	}
}

func TestUncommittedPreparedRecordsAreNotSilentlySkipped(t *testing.T) {
	events := []Event{{Type: EventPrepare, TxID: "open", Record: recordPtr(object("o1"))}}
	_, err := NewExporter(events).Export(0, NoLimits)
	if exportErr, ok := err.(*ExportError); !ok || exportErr.Class != ClassAtomicity {
		t.Fatalf("expected atomicity error, got %v", err)
	}
}

func TestCoordinatorStopsAfterLiveResourceLimit(t *testing.T) {
	coordinator := NewCoordinator()
	stream := coordinator.Export(AutoBoundary, Limits{MaxTransactions: 1, MaxRecords: 1})
	<-stream

	if err := coordinator.Begin("first", object("o1")); err != nil {
		t.Fatal(err)
	}
	lsn, err := coordinator.Commit("first")
	if err != nil || lsn != 1 {
		t.Fatalf("first commit should be accepted, lsn=%d err=%v", lsn, err)
	}
	firstDelta := <-stream
	if len(firstDelta.Deltas) != 1 {
		t.Fatalf("expected first delta, got %#v", firstDelta)
	}

	if err := coordinator.Begin("second", object("o2")); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Commit("second"); err == nil {
		t.Fatal("second commit should be rejected by the resource limit")
	}
	errorFrame := <-stream
	if errorFrame.Error == nil || errorFrame.Error.Class != ClassResource {
		t.Fatalf("expected resource frame, got %#v", errorFrame)
	}
}

func TestBoundaryClassificationIsConstantWork(t *testing.T) {
	manager := NewBoundaryManager()
	if testing.Short() {
		t.Skip("constant-work classification check only runs in the full suite")
	}
	before := testing.AllocsPerRun(1000, func() {
		manager.Classify(1_000_001, 1_000_000)
	})
	if before != 0 {
		t.Fatalf("classification allocated %v times; it must compare two integers", before)
	}
}

func TestRandomSequencesMatchNaiveReferenceModel(t *testing.T) {
	random := rand.New(rand.NewSource(20261007))
	for iteration := 0; iteration < 500; iteration++ {
		input := generateValidHistory(random, 1+random.Intn(20))
		boundary := int64(0)
		if commits := committedLSNs(input); len(commits) > 0 {
			boundary = commits[random.Intn(len(commits))]
		}
		actual, err := NewExporter(input).Export(boundary, NoLimits)
		expected := naiveExport(t, input, boundary)
		if err != nil {
			t.Fatalf("case %d input=%v unexpected error=%v", iteration, input, err)
		}
		trace := fmt.Sprintf("case=%d input=%v actual=%#v rules=left-closed-lsn;whole-tx-at-commit;link-endpoints-required", iteration, input, actual)
		t.Log(trace)
		if !sameExport(actual, expected) {
			t.Fatalf("case %d\ninput=%v\nexpected=%#v\nactual=%#v\nrules=left-closed-lsn;whole-tx-at-commit;link-endpoints-required\ntrace=%s", iteration, input, expected, actual, trace)
		}
	}
}

func TestCoordinatorSerializesConcurrentWritesAndSnapshotRead(t *testing.T) {
	coordinator := NewCoordinator()
	if err := coordinator.Begin("seed", object("seed")); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Commit("seed"); err != nil {
		t.Fatal(err)
	}

	stream := coordinator.Export(AutoBoundary, NoLimits)
	snapshotFrame := <-stream
	if snapshotFrame.Snapshot == nil || snapshotFrame.Snapshot.Boundary != 1 {
		t.Fatalf("unexpected initial stream frame: %#v", snapshotFrame)
	}

	const writers = 24
	var waiter sync.WaitGroup
	writerErrors := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		waiter.Add(1)
		go func(index int) {
			defer waiter.Done()
			txID := fmt.Sprintf("tx-%02d", index)
			objectID := fmt.Sprintf("object-%02d", index)
			linkID := fmt.Sprintf("link-%02d", index)
			if err := coordinator.Begin(txID, object(objectID), link(linkID, objectID, objectID)); err != nil {
				writerErrors <- err
				return
			}
			if _, err := coordinator.Commit(txID); err != nil {
				writerErrors <- err
			}
		}(writer)
	}

	received := make(chan Delta, writers)
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		for index := 0; index < writers; index++ {
			frame := <-stream
			if frame.Error != nil {
				t.Errorf("stream error: %v", frame.Error)
				return
			}
			if len(frame.Deltas) != 1 || len(frame.Deltas[0].Records) != 2 {
				t.Errorf("transaction was split: %#v", frame)
				return
			}
			received <- frame.Deltas[0]
		}
	}()
	waiter.Wait()
	close(writerErrors)
	for err := range writerErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	<-collectorDone
	close(received)

	state := cloneSnapshot(snapshotFrame.Snapshot)
	seenLSNs := map[int64]bool{}
	previous := snapshotFrame.Snapshot.Boundary
	for delta := range received {
		if delta.CommitLSN != previous+1 {
			t.Fatalf("delta order gap: previous=%d frame=%#v", previous, delta)
		}
		if seenLSNs[delta.CommitLSN] {
			t.Fatalf("duplicate delta LSN %d", delta.CommitLSN)
		}
		seenLSNs[delta.CommitLSN] = true
		previous = delta.CommitLSN
		for _, record := range delta.Records {
			applyRecord(state, record)
		}
		if len(state.Links) != len(state.Objects)-1 {
			t.Fatalf("link visibility outran object endpoints: objects=%d links=%d", len(state.Objects), len(state.Links))
		}
	}
	if len(seenLSNs) != writers || previous != int64(writers+1) {
		t.Fatalf("expected %d ordered deltas ending at %d, got %d ending at %d", writers, writers+1, len(seenLSNs), previous)
	}
}

func generateValidHistory(random *rand.Rand, commitCount int) []Event {
	events := make([]Event, 0)
	objectIDs := []string{}
	pending := map[string][]Record{}
	for len(committedLSNs(events)) < commitCount {
		txID := fmt.Sprintf("tx%d", len(committedLSNs(events))+1)
		records := make([]Record, 0, 2)
		if len(committedLSNs(events)) == 0 || random.Intn(3) == 0 {
			id := fmt.Sprintf("o%d", len(objectIDs))
			objectIDs = append(objectIDs, id)
			records = append(records, object(id))
		}
		if random.Intn(2) == 0 && len(objectIDs) > 1 {
			source := objectIDs[random.Intn(len(objectIDs))]
			target := objectIDs[random.Intn(len(objectIDs))]
			records = append(records, link(fmt.Sprintf("l%d", len(events)+1), source, target))
		}
		if len(records) == 0 {
			records = append(records, action(fmt.Sprintf("a%d", len(committedLSNs(events))+1)))
		}
		pending[txID] = records
		for _, record := range records {
			copyRecord := record
			events = append(events, Event{Type: EventPrepare, TxID: txID, Record: &copyRecord})
		}
		events = append(events, Event{Type: EventCommit, TxID: txID, CommitLSN: int64(len(committedLSNs(events)) + 1)})
	}
	return events
}

func naiveExport(t *testing.T, events []Event, boundary int64) []ExportFrame {
	t.Helper()
	groups := map[string][]Record{}
	var lsnOrder []int64
	committed := map[int64]Delta{}
	for _, event := range events {
		if event.Type == EventPrepare {
			groups[event.TxID] = append(groups[event.TxID], *event.Record)
		}
		if event.Type == EventCommit {
			lsnOrder = append(lsnOrder, event.CommitLSN)
			committed[event.CommitLSN] = Delta{CommitLSN: event.CommitLSN, TxID: event.TxID, Records: groups[event.TxID]}
		}
	}
	if _, ok := committed[boundary]; boundary != 0 && !ok {
		t.Fatalf("naive model got invalid boundary %d", boundary)
	}
	state := newSnapshot(boundary)
	for _, lsn := range lsnOrder {
		if lsn > boundary {
			continue
		}
		delta := committed[lsn]
		for _, record := range delta.Records {
			applyRecord(state, record)
		}
	}
	frames := []ExportFrame{{Snapshot: state}}
	for _, lsn := range lsnOrder {
		if lsn <= boundary {
			continue
		}
		delta := committed[lsn]
		next := cloneSnapshot(state)
		for _, record := range delta.Records {
			applyRecord(next, record)
		}
		for _, linkRecord := range next.Links {
			if _, sourceOK := next.Objects[linkRecord.SourceID]; !sourceOK || strings.TrimSpace(linkRecord.SourceID) == "" {
				t.Fatalf("naive model detected integrity failure: %#v", linkRecord)
			}
			if _, targetOK := next.Objects[linkRecord.TargetID]; !targetOK || strings.TrimSpace(linkRecord.TargetID) == "" {
				t.Fatalf("naive model detected integrity failure: %#v", linkRecord)
			}
		}
		state = next
		frames = append(frames, ExportFrame{Deltas: []Delta{delta}})
	}
	frames = append(frames, ExportFrame{Done: true})
	return frames
}

func committedLSNs(events []Event) []int64 {
	result := make([]int64, 0)
	for _, event := range events {
		if event.Type == EventCommit {
			result = append(result, event.CommitLSN)
		}
	}
	return result
}

func sameExport(actual, expected []ExportFrame) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if !sameSnapshot(actual[index].Snapshot, expected[index].Snapshot) || !sameDeltas(actual[index].Deltas, expected[index].Deltas) || actual[index].Done != expected[index].Done {
			return false
		}
	}
	return true
}

func sameSnapshot(actual, expected *Snapshot) bool {
	if (actual == nil) != (expected == nil) {
		return false
	}
	if actual == nil {
		return true
	}
	return actual.Boundary == expected.Boundary && sameRecordMap(actual.Objects, expected.Objects) && sameRecordMap(actual.Links, expected.Links) && sameRecordMap(actual.Actions, expected.Actions)
}

func sameRecordMap(actual, expected map[string]Record) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if !reflect.DeepEqual(actual[key], value) {
			return false
		}
	}
	return true
}

func sameDeltas(actual, expected []Delta) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index].CommitLSN != expected[index].CommitLSN || actual[index].TxID != expected[index].TxID || len(actual[index].Records) != len(expected[index].Records) {
			return false
		}
		for recordIndex := range expected[index].Records {
			if !reflect.DeepEqual(actual[index].Records[recordIndex], expected[index].Records[recordIndex]) {
				return false
			}
		}
	}
	return true
}

func recordPtr(record Record) *Record { return &record }
