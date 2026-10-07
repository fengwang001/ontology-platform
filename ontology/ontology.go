package ontology

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

type Platform struct {
	store *store
	wal   *wal
	log   Logger

	txSeq atomic.Uint64

	crashMu sync.Mutex
	crash   CrashHook
}

func New(schema Schema, logger Logger) *Platform {
	return &Platform{store: newStore(schema), wal: &wal{}, log: logger}
}

func Open(schema Schema, walPath string, logger Logger) (*Platform, error) {
	s := newStore(schema)
	recovered, rec, err := recoverWAL(s, walPath)
	if err != nil {
		return nil, err
	}
	w, err := openWAL(walPath)
	if err != nil {
		return nil, err
	}
	p := &Platform{store: s, wal: w, log: logger}
	if recovered {
		p.emit(Event{Op: "recover", Property: rec.Property, TxID: rec.TxID, Committed: false, Recovered: true, Reason: "uncommitted transaction rolled back from write-ahead log", AttemptedChanges: changesFromRecord(rec)})
	}
	return p, nil
}

func (p *Platform) CreateObject(id string) bool {
	if !p.store.CreateObject(id) {
		return false
	}
	if p.wal.path == "" {
		return true
	}
	if err := p.store.saveSnapshot(snapshotPath(p.wal.path)); err != nil {
		panic(err)
	}
	if err := os.Remove(preSnapshotPath(p.wal.path)); err != nil && !os.IsNotExist(err) {
		panic(err)
	}
	if err := p.wal.truncate(); err != nil {
		panic(err)
	}
	return true
}

func (p *Platform) WriteProperty(ctx context.Context, property string, writes []PropertyWrite) error {
	txID := p.nextTxID()
	changes := map[string]EntryChange{}
	def, supported := p.store.schema.Properties[property]
	if len(writes) == 0 {
		return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes}, writeError(ErrBatchRollback, "", errors.New("empty batch write")))
	}
	validation := validateWrites(p.store, property, supported, writes)
	if len(validation) > 0 {
		return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes}, batchPriority(validation, len(writes) > 1))
	}

	ordered := append([]PropertyWrite(nil), writes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Object < ordered[j].Object })
	objectLocks := make([]*sync.RWMutex, 0, len(ordered))
	propertyLocks := make([]*sync.RWMutex, 0, len(ordered))
	for _, write := range ordered {
		objectLock := p.store.lockObject(write.Object)
		objectLock.RLock()
		objectLocks = append(objectLocks, objectLock)
		propertyLock := p.store.lockProperty(write.Object, property)
		propertyLock.Lock()
		propertyLocks = append(propertyLocks, propertyLock)
	}
	indexLocks := lockIndexes(p.store, indexDefNames(def.Indexes))
	defer unlockAll(indexLocks, propertyLocks, objectLocks, true)

	for _, write := range ordered {
		if !p.store.Exists(write.Object) {
			err := writeError(ErrObjectNotFound, write.Object, errors.New("object instance no longer exists"))
			if len(writes) > 1 {
				err = batchPriority([]*WriteError{err}, true)
			}
			return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes}, err)
		}
	}

	items := make([]txItem, 0, len(ordered))
	failures := make([]*WriteError, 0)
	for _, write := range ordered {
		oldValue, _ := p.store.GetLocked(write.Object, property)
		item := txItem{Object: write.Object, Property: property, OldValue: oldValue, NewValue: write.Value, OldExists: true, NewExists: true}
		for _, indexDef := range def.Indexes {
			oldKey := effectiveKey(indexDef, oldValue)
			newKey := effectiveKey(indexDef, write.Value)
			membership := indexMembership{Index: indexDef.Name, Property: property, OldKey: oldKey, NewKey: newKey}
			if _, ok := p.store.indexes[indexDef.Name][property][oldKey][write.Object]; ok {
				membership.OldExist = true
			}
			if indexDef.Fail {
				failures = append(failures, writeError(ErrIndexMaintenance, write.Object, fmt.Errorf("index %q maintenance failed", indexDef.Name)))
			}
			item.Memberships = append(item.Memberships, membership)
		}
		items = append(items, item)
	}

	rec := walRecord{TxID: txID, State: "begin", Op: txWriteProperty, Property: property, Items: items}
	if err := p.saveBeforeSnapshot(); err != nil {
		p.undoItems(items)
		return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes}, writeError(ErrIndexMaintenance, "", err))
	}
	if err := p.wal.append(rec); err != nil {
		p.undoItems(items)
		return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes}, writeError(ErrIndexMaintenance, "", err))
	}
	if err := p.runCrash(ctx, txID, CrashAfterBegin); err != nil {
		return p.crashRecover(err)
	}

	for _, item := range items {
		for _, membership := range item.Memberships {
			if membership.OldKey != membership.NewKey {
				if membership.OldExist {
					p.store.removeEntryLocked(membership.Index, property, membership.OldKey, item.Object)
					appendChange(changes, indexEntry(membership.Index, property, membership.OldKey, item.Object), false)
				}
				p.store.addEntryLocked(membership.Index, property, membership.NewKey, item.Object)
				appendChange(changes, indexEntry(membership.Index, property, membership.NewKey, item.Object), true)
			}
		}
	}
	if len(failures) > 0 {
		attempted := cloneChanges(changes)
		p.undoItems(items)
		return p.reject(Event{Op: "write", Property: property, TxID: txID, Writes: writes, AttemptedChanges: attempted}, batchPriority(failures, len(writes) > 1))
	}
	if err := p.runCrash(ctx, txID, CrashAfterIndex); err != nil {
		return p.crashRecover(err)
	}
	for _, item := range items {
		p.store.SetValueLocked(item.Object, property, item.NewValue)
	}
	if err := p.runCrash(ctx, txID, CrashAfterData); err != nil {
		return p.crashRecover(err)
	}
	if err := p.finishRecord(ctx, rec, txID); err != nil {
		return err
	}

	p.emit(Event{Op: "write", Property: property, TxID: txID, Writes: writes, Committed: true, IndexChanges: changes})
	return nil
}

func (p *Platform) DeleteObject(ctx context.Context, id string) error {
	txID := p.nextTxID()
	if !p.store.Exists(id) {
		return p.reject(Event{Op: "delete", Object: id, TxID: txID}, writeError(ErrObjectNotFound, id, errors.New("object instance does not exist")))
	}
	properties := p.store.sortedPropertyNames()
	objectLock := p.store.lockObject(id)
	objectLock.Lock()
	defer objectLock.Unlock()
	propertyLocks := make([]*sync.RWMutex, 0, len(properties))
	for _, property := range properties {
		propertyLock := p.store.lockProperty(id, property)
		propertyLock.Lock()
		propertyLocks = append(propertyLocks, propertyLock)
	}
	defer func() {
		for _, lock := range propertyLocks {
			lock.Unlock()
		}
	}()
	indexLocks := lockIndexes(p.store, p.indexNamesFor(properties))
	defer unlockIndexes(indexLocks)

	current := p.store.allValuesLocked(id)
	items := make([]txItem, 0, len(properties))
	changes := map[string]EntryChange{}
	for _, property := range properties {
		def := p.store.schema.Properties[property]
		item := txItem{Object: id, Property: property, OldValue: current[property], NewValue: Missing(), OldExists: true, NewExists: false}
		for _, indexDef := range def.Indexes {
			oldKey := effectiveKey(indexDef, current[property])
			membership := indexMembership{Index: indexDef.Name, Property: property, OldKey: oldKey, NewKey: MissingKey()}
			if _, ok := p.store.indexes[indexDef.Name][property][oldKey][id]; ok {
				membership.OldExist = true
			}
			item.Memberships = append(item.Memberships, membership)
		}
		items = append(items, item)
	}
	rec := walRecord{TxID: txID, State: "begin", Op: txDeleteObject, Items: items}
	if err := p.saveBeforeSnapshot(); err != nil {
		p.undoItems(items)
		return p.reject(Event{Op: "delete", Object: id, TxID: txID}, writeError(ErrIndexMaintenance, id, err))
	}
	if err := p.wal.append(rec); err != nil {
		p.undoItems(items)
		return p.reject(Event{Op: "delete", Object: id, TxID: txID}, writeError(ErrIndexMaintenance, id, err))
	}
	if err := p.runCrash(ctx, txID, CrashAfterBegin); err != nil {
		return p.crashRecover(err)
	}
	for _, item := range items {
		for _, membership := range item.Memberships {
			if membership.OldExist {
				p.store.removeEntryLocked(membership.Index, item.Property, membership.OldKey, id)
				appendChange(changes, indexEntry(membership.Index, item.Property, membership.OldKey, id), false)
			}
		}
	}
	if err := p.runCrash(ctx, txID, CrashAfterIndex); err != nil {
		return p.crashRecover(err)
	}
	p.store.setDeletedLocked(id)
	if err := p.runCrash(ctx, txID, CrashAfterData); err != nil {
		return p.crashRecover(err)
	}
	if err := p.finishRecord(ctx, rec, txID); err != nil {
		return err
	}
	p.emit(Event{Op: "delete", Object: id, TxID: txID, Committed: true, IndexChanges: changes})
	return nil
}

func (p *Platform) Get(id string, property string) (Value, error) {
	if _, ok := p.store.schema.Properties[property]; !ok {
		return Value{}, writeError(ErrPropertyNotIndexed, id, fmt.Errorf("property %q is not indexed", property))
	}
	if !p.store.Exists(id) {
		return Value{}, writeError(ErrObjectNotFound, id, errors.New("object instance does not exist"))
	}
	objectLock := p.store.lockObject(id)
	objectLock.RLock()
	defer objectLock.RUnlock()
	propertyLock := p.store.lockProperty(id, property)
	propertyLock.RLock()
	defer propertyLock.RUnlock()
	value, _ := p.store.GetLocked(id, property)
	return value, nil
}

func (p *Platform) Query(indexName, property string, key IndexKey) ([]string, error) {
	def, ok := p.store.schema.Properties[property]
	if !ok {
		return nil, writeError(ErrPropertyNotIndexed, "", fmt.Errorf("property %q is not indexed", property))
	}
	if !indexDefExists(def.Indexes, indexName) {
		return nil, writeError(ErrPropertyNotIndexed, "", fmt.Errorf("property %q does not use index %q", property, indexName))
	}
	return p.store.IndexQuery(indexName, property, key), nil
}

func (p *Platform) Clock() uint64 { return atomic.LoadUint64(&p.store.clock) }

func (p *Platform) SetCrashHook(hook CrashHook) {
	p.crashMu.Lock()
	defer p.crashMu.Unlock()
	p.crash = hook
}

func (p *Platform) Close() error { return p.wal.Close() }

func (p *Platform) RecoverInMemory() error {
	recovered, rec, err := recoverWAL(p.store, p.wal.path)
	if err != nil || !recovered {
		return err
	}
	p.emit(Event{Op: "recover", Property: rec.Property, TxID: rec.TxID, Recovered: true, Committed: false, Reason: "uncommitted transaction rolled back from write-ahead log", AttemptedChanges: changesFromRecord(rec)})
	return nil
}

func (p *Platform) finishRecord(ctx context.Context, rec walRecord, txID uint64) error {
	rec.State = "prepare"
	if err := p.wal.append(rec); err != nil {
		_ = p.RecoverInMemory()
		return writeError(ErrIndexMaintenance, "", err)
	}
	if err := p.runCrash(ctx, txID, CrashAfterPrepare); err != nil {
		return p.crashRecover(err)
	}
	if p.wal.path != "" {
		atomic.AddUint64(&p.store.clock, 1)
		if err := p.store.saveSnapshot(snapshotPath(p.wal.path)); err != nil {
			atomic.AddUint64(&p.store.clock, ^uint64(0))
			_ = p.RecoverInMemory()
			return writeError(ErrIndexMaintenance, "", err)
		}
		if err := os.Remove(preSnapshotPath(p.wal.path)); err != nil && !os.IsNotExist(err) {
			return writeError(ErrIndexMaintenance, "", err)
		}
	} else {
		atomic.AddUint64(&p.store.clock, 1)
	}
	rec.State = "commit"
	if err := p.wal.append(rec); err != nil {
		_ = p.RecoverInMemory()
		return writeError(ErrIndexMaintenance, "", err)
	}
	if err := p.wal.truncate(); err != nil {
		_ = p.RecoverInMemory()
		return writeError(ErrIndexMaintenance, "", err)
	}
	return p.runCrash(ctx, txID, CrashAfterCommit)
}

func (p *Platform) crashRecover(crash error) error {
	_ = p.RecoverInMemory()
	return crash
}

func (p *Platform) reject(event Event, err *WriteError) error {
	event.Reason = err.Error()
	if event.IndexChanges == nil {
		event.IndexChanges = map[string]EntryChange{}
	}
	p.emit(event)
	return err
}

func (p *Platform) nextTxID() uint64 { return p.txSeq.Add(1) }

func (p *Platform) runCrash(ctx context.Context, txID uint64, point CrashPoint) error {
	p.crashMu.Lock()
	hook := p.crash
	p.crashMu.Unlock()
	if hook != nil && hook(ctx, point) {
		return errCrash
	}
	return nil
}

func (p *Platform) emit(event Event) {
	if p.log != nil {
		p.log.Log(event)
	}
}

func (p *Platform) undoItems(items []txItem) { undoTransaction(p.store, walRecord{Items: items}) }

func (p *Platform) saveBeforeSnapshot() error {
	if p.wal.path == "" {
		return nil
	}
	return backupSnapshot(p.wal.path)
}

func validateWrites(s *store, property string, supported bool, writes []PropertyWrite) []*WriteError {
	errs := make([]*WriteError, 0)
	seen := map[string]struct{}{}
	for _, write := range writes {
		if !s.Exists(write.Object) {
			errs = append(errs, writeError(ErrObjectNotFound, write.Object, errors.New("object instance does not exist")))
			continue
		}
		if !supported {
			errs = append(errs, writeError(ErrPropertyNotIndexed, write.Object, fmt.Errorf("property %q is not indexed", property)))
			continue
		}
		if _, duplicate := seen[write.Object]; duplicate {
			errs = append(errs, writeError(ErrIndexMaintenance, write.Object, errors.New("object appears more than once in one batch")))
			continue
		}
		seen[write.Object] = struct{}{}
	}
	return errs
}

func batchPriority(errs []*WriteError, batch bool) *WriteError {
	err := priorityError(errs)
	if !batch {
		return err
	}
	copyErr := *err
	copyErr.Code = ErrBatchRollback
	copyErr.Reason = fmt.Errorf("batch rolled back: %w", err.Reason)
	return &copyErr
}

func effectiveKey(def IndexDef, value Value) IndexKey {
	if !value.Present {
		return MissingKey()
	}
	key := def.Key(value)
	key.Present = true
	return key
}
