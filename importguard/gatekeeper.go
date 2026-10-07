package importguard

import (
	"fmt"
	"sort"
	"sync"
)

// Gatekeeper is the batch-import attribute-level permission gatekeeper.
//
// # Concurrency model
//
// The whole BatchImport runs inside the store's mutex, making each batch one
// critical section. Therefore any set of concurrent batches is equivalent to
// some global serial order, and every batch also reads its own batch-start
// permission snapshot (deep copy + folded permission index), so permission
// changes that occur while a batch is in flight cannot affect it.
//
// Within one batch, record judgments run in a bounded worker pool to honor
// the "records are judged concurrently" requirement; because all reads are
// immutable (snapshot + a sequentially-built, per-index base overlay for
// same-batch duplicates) and all state mutations are applied afterwards in
// record order by a single committer, the outcome is identical to processing
// records strictly serially in index order.
type Gatekeeper struct {
	store  *Store
	logger DecisionLogger
}

// NewGatekeeper builds a gatekeeper over store. If logger is nil, decision
// logs are discarded.
func NewGatekeeper(store *Store, logger DecisionLogger) *Gatekeeper {
	if logger == nil {
		logger = nopLogger{}
	}
	return &Gatekeeper{store: store, logger: logger}
}

type nopLogger struct{}

func (nopLogger) LogDecision(DecisionRecord) {}

func (g *Gatekeeper) log(stage string, inputs map[string]any, output, basis string) {
	g.logger.LogDecision(DecisionRecord{Stage: stage, Inputs: inputs, Output: output, Basis: basis})
}

// BatchImport executes a batch import under subjectID with the declared mode.
// Batch-level rejection (illegal mode, then missing subject) means no record
// is processed and no state changes.
func (g *Gatekeeper) BatchImport(subjectID string, mode Mode, entries []Entry) BatchResult {
	// Batch-level checks happen before taking the global critical section;
	// they read no mutable state except the mode.
	if mode != ModeAtomic && mode != ModeLenient {
		f := &Failure{
			Category: ErrBatchInvalidParameter,
			Message:  fmt.Sprintf("missing or illegal import mode %q; must be %q or %q", mode, ModeAtomic, ModeLenient),
		}
		g.log("batch.validate_mode", map[string]any{"mode": string(mode)}, "reject_batch", f.Message)
		return BatchResult{Rejected: true, Failure: f}
	}

	// The entire batch — snapshot, all judgments and all commits — is one
	// critical section, which guarantees global serializability.
	g.store.mu.Lock()
	defer g.store.mu.Unlock()

	snap := takeSnapshotLocked(g.store)
	g.log("batch.snapshot", map[string]any{
		"subject": subjectID, "mode": string(mode), "records": len(entries),
	}, "snapshot_taken", fmt.Sprintf("folded %d historical permission entries into effective-permission index", snap.historyCount))

	if !snap.subjectExists(subjectID) {
		f := &Failure{
			Category: ErrSubjectNotFound,
			Message:  fmt.Sprintf("initiating subject %q does not exist at batch start", subjectID),
		}
		g.log("batch.validate_subject", map[string]any{"subject": subjectID}, "reject_batch", f.Message)
		return BatchResult{Rejected: true, Failure: f}
	}

	results := make([]RecordResult, len(entries))

	// Phase 1: judge every record concurrently against immutable inputs.
	// Permission decisions (the part protected by the start-of-batch
	// snapshot) are fully determined by the snapshot and never revisit live
	// state. Existence/required checks that may observe earlier same-batch
	// writes are re-applied serially against the overlay in phase 2, so the
	// final effect equals serial index-order processing.
	judged := make([]*judgedRecord, len(entries))
	var wg sync.WaitGroup
	workers := 4
	if len(entries) < workers {
		workers = len(entries)
	}
	if workers == 0 {
		workers = 1
	}
	ch := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				var probes int
				judged[i] = g.judge(subjectID, mode, i, entries[i], snap, &probes)
			}
		}()
	}
	for i := range entries {
		ch <- i
	}
	close(ch)
	wg.Wait()

	// Phase 2: commit strictly in index order. Same-batch records that target
	// the same ObjectID are re-validated for existence against the serial
	// overlay so their final effect matches serial processing; earlier
	// accepted creates/updates advance the overlay.
	overlay := map[string]*Object{}
	for id, o := range snap.objects {
		ov := *o
		overlay[id] = &ov
	}
	for i, jr := range judged {
		results[i] = g.commit(subjectID, mode, i, entries[i], jr, snap, overlay)
	}

	return BatchResult{Rejected: false, Records: results}
}

// judgedRecord is the immutable output of phase 1 for one record.
type judgedRecord struct {
	result      RecordResult
	writeFields map[string]PropertyValue
	denied      []string
	permLookups int
	atomic      bool
}

// judge performs the record-level decision pipeline:
//
//	type existence > semantic match > field permissions (mode-dependent) >
//	required-property re-check (lenient)
//
// It never mutates store state.
func (g *Gatekeeper) judge(subjectID string, mode Mode, idx int, e Entry, snap *Snapshot, probes *int) *judgedRecord {
	inputs := map[string]any{
		"index": idx, "object": e.ObjectID, "type": e.Type,
		"semantic": string(e.Semantic), "fields": sortedKeys(e.Fields),
	}
	fail := func(cat ErrorCategory, msg string, fields ...string) *judgedRecord {
		f := &Failure{Category: cat, Message: msg}
		if len(fields) > 0 {
			f.Fields = fields
		}
		g.log("record."+string(cat), inputs, "record_failed", msg)
		return &judgedRecord{result: RecordResult{
			ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f,
		}}
	}

	// 1) Object type existence.
	otype, ok := snap.objectType(e.Type)
	if !ok {
		return fail(ErrObjectTypeNotFound, fmt.Sprintf("object type %q does not exist", e.Type))
	}

	// Phase 1 only decides field permissions against the snapshot. Semantic
	// and required checks (which may observe earlier same-batch writes) are
	// applied serially in commit(). An illegal semantic is a static failure.
	if e.Semantic != SemanticCreate && e.Semantic != SemanticUpdate {
		return fail(ErrSemanticMismatch, fmt.Sprintf("illegal semantic %q", e.Semantic))
	}

	// 2) Field-level write permissions. Exactly one snapshot lookup per field.
	allowed := make(map[string]PropertyValue, len(e.Fields))
	denied := map[string]bool{}
	keys := sortedKeys(e.Fields)
	for _, f := range keys {
		ok, basis := snap.canWrite(subjectID, e.Type, f, probes)
		g.log("record.field_permission", map[string]any{
			"index": idx, "object": e.ObjectID, "field": f, "mode": string(mode),
		}, map[bool]string{true: "writable", false: "denied"}[ok], basis)
		if ok {
			allowed[f] = e.Fields[f]
		} else {
			denied[f] = true
		}
	}

	written := sortedKeys(allowed)
	jr := &judgedRecord{writeFields: allowed, denied: mapKeysSorted(denied), atomic: mode == ModeAtomic}
	jr.result = RecordResult{ObjectID: e.ObjectID, Index: idx, WrittenKeys: written}
	jr.permLookups = *probes
	g.log("record.permission_phase", inputs, "permissions_decided",
		fmt.Sprintf("writable=%v denied=%v mode=%s (type=%s, required=%v)", written, jr.denied, mode, e.Type, mapKeysBool(otype.Required)))
	return jr
}

// commit applies an accepted judged record to the serial overlay and the
// store in index order. It applies, in the required priority:
// type existence (already assured, re-checked against snapshot), semantic
// match against the serial overlay, then required-property re-evaluation
// after skipping. Failures commit nothing and change no state.
func (g *Gatekeeper) commit(subjectID string, mode Mode, idx int, e Entry, jr *judgedRecord, snap *Snapshot, overlay map[string]*Object) RecordResult {
	if jr.result.Status == StatusFailed {
		// Nothing written; overlay unchanged.
		g.log("commit", map[string]any{"index": idx, "object": e.ObjectID}, "skipped_failed", "failed record commits no state change")
		return jr.result
	}
	otype, ok := snap.objectType(e.Type)
	if !ok {
		f := &Failure{Category: ErrObjectTypeNotFound, Message: fmt.Sprintf("object type %q does not exist", e.Type)}
		g.log("record."+string(ErrObjectTypeNotFound), map[string]any{"index": idx, "type": e.Type}, "record_failed", f.Message)
		return RecordResult{ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f}
	}
	base, overlayExists := overlay[e.ObjectID]
	inputs := map[string]any{"index": idx, "object": e.ObjectID, "type": e.Type, "semantic": string(e.Semantic)}
	if e.Semantic == SemanticCreate && overlayExists {
		f := &Failure{Category: ErrSemanticMismatch,
			Message: fmt.Sprintf("create declared for already-existing object %q (created earlier in the same batch or pre-existing)", e.ObjectID)}
		g.log("record."+string(ErrSemanticMismatch), inputs, "record_failed", f.Message)
		return RecordResult{ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f}
	}
	if e.Semantic == SemanticUpdate && !overlayExists {
		f := &Failure{Category: ErrSemanticMismatch,
			Message: fmt.Sprintf("update declared for non-existent object %q", e.ObjectID)}
		g.log("record."+string(ErrSemanticMismatch), inputs, "record_failed", f.Message)
		return RecordResult{ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f}
	}

	// 3) Field-permission stage (mode dependent), evaluated after the
	// semantic match so the specified rejection priority always holds.
	if jr.atomic && len(jr.denied) > 0 {
		f := &Failure{Category: ErrFieldPermissionDenied,
			Message: fmt.Sprintf("atomic mode: non-writable field(s) %v reject the whole record; nothing is written", jr.denied),
			Fields:  append([]string(nil), jr.denied...)}
		g.log("record."+string(ErrFieldPermissionDenied), inputs, "record_failed", f.Message)
		return RecordResult{ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f}
	}

	// Required-property constraint re-evaluation after field skipping, using
	// the object state visible at this serial point. Create has no old value;
	// update may reuse the pre-existing value on base.
	var missing []string
	for req := range otype.Required {
		if _, writing := jr.writeFields[req]; writing {
			continue
		}
		if e.Semantic == SemanticUpdate && base != nil {
			if _, hasOld := base.Properties[req]; hasOld {
				continue
			}
		}
		missing = append(missing, req)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		f := &Failure{Category: ErrRequiredPropertyMissing,
			Message: fmt.Sprintf("required properties %v cannot be satisfied after field skipping (no pre-existing value under %s semantics)",
				missing, e.Semantic),
			Fields: missing}
		g.log("record."+string(ErrRequiredPropertyMissing), inputs, "record_failed", f.Message)
		return RecordResult{ObjectID: e.ObjectID, Index: idx, Status: StatusFailed, Failure: f}
	}

	if e.Semantic == SemanticCreate {
		g.store.insertObjectLocked(e.ObjectID, e.Type, jr.writeFields)
		props := make(map[string]PropertyValue, len(jr.writeFields))
		for k, v := range jr.writeFields {
			props[k] = v
		}
		overlay[e.ObjectID] = &Object{ID: e.ObjectID, Type: e.Type, Properties: props, Version: 1}
	} else {
		g.store.patchObjectLocked(e.ObjectID, jr.writeFields)
		for k, v := range jr.writeFields {
			overlay[e.ObjectID].Properties[k] = v
		}
		overlay[e.ObjectID].Version++
	}
	status := StatusSuccess
	result := RecordResult{ObjectID: e.ObjectID, Index: idx, WrittenKeys: jr.result.WrittenKeys}
	if len(jr.denied) > 0 {
		status = StatusPartial
		result.Skipped = jr.denied
	}
	result.Status = status
	g.log("commit", map[string]any{"index": idx, "object": e.ObjectID, "written": result.WrittenKeys, "skipped": result.Skipped, "subject": subjectID},
		string(status), "state change applied in serial index order; field decisions from batch-start snapshot")
	return result
}

func sortedKeys(m map[string]PropertyValue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mapKeysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mapKeysBool(m map[string]bool) []string { return mapKeysSorted(m) }
