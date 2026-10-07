package ontology

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const (
	txWriteProperty = "write_property"
	txDeleteObject  = "delete_object"
)

type indexMembership struct {
	Index    string
	Property string
	OldKey   IndexKey
	NewKey   IndexKey
	OldExist bool
}

type txItem struct {
	Object      string
	Property    string
	OldValue    Value
	NewValue    Value
	OldExists   bool
	NewExists   bool
	Memberships []indexMembership
}

type walRecord struct {
	TxID     uint64   `json:"tx_id"`
	State    string   `json:"state"`
	Op       string   `json:"op"`
	Property string   `json:"property,omitempty"`
	Items    []txItem `json:"items,omitempty"`
}

type snapshotData struct {
	Clock   uint64                              `json:"clock"`
	Objects map[string]map[string]snapshotValue `json:"objects"`
}

type snapshotValue struct {
	Present bool `json:"present"`
	Data    any  `json:"data"`
}

type wal struct {
	path string
	f    *os.File
}

func openWAL(path string) (*wal, error) {
	if path == "" {
		return &wal{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &wal{path: path, f: f}, nil
}

func (w *wal) append(rec walRecord) error {
	if w == nil || w.f == nil {
		return nil
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *wal) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

func (w *wal) truncate() error {
	if w == nil || w.f == nil {
		return nil
	}
	if err := w.f.Truncate(0); err != nil {
		return err
	}
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return w.f.Sync()
}

func snapshotPath(walPath string) string { return walPath + ".snapshot" }

func preSnapshotPath(walPath string) string { return walPath + ".pre" }

func backupSnapshot(walPath string) error {
	if walPath == "" {
		return nil
	}
	payload, err := os.ReadFile(snapshotPath(walPath))
	if os.IsNotExist(err) {
		payload = nil
	} else if err != nil {
		return err
	}
	return os.WriteFile(preSnapshotPath(walPath), payload, 0o600)
}

func (s *store) saveSnapshot(path string) error {
	s.mu.RLock()
	data := snapshotData{Clock: s.clock, Objects: make(map[string]map[string]snapshotValue)}
	for id, obj := range s.objects {
		if !obj.exists.Load() {
			continue
		}
		values := make(map[string]snapshotValue, len(obj.values))
		for property, slot := range obj.values {
			value := slot.Load().(Value)
			values[property] = snapshotValue{Present: value.Present, Data: value.Data}
		}
		data.Objects[id] = values
	}
	s.mu.RUnlock()

	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadSnapshot(s *store, path string) error {
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if len(payload) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var data snapshotData
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.objects {
		for _, prop := range s.sortedPropertyNames() {
			for _, indexDef := range s.schema.Properties[prop].Indexes {
				value := s.objects[id].values[prop].Load().(Value)
				key := effectiveKey(indexDef, value)
				s.removeEntryLocked(indexDef.Name, prop, key, id)
			}
		}
	}
	s.objects = make(map[string]*objectState)
	s.propMu = make(map[string]*sync.RWMutex)
	s.objMu = make(map[string]*sync.RWMutex)
	s.indexes = make(map[string]map[string]map[IndexKey]map[string]struct{})
	for _, prop := range s.sortedPropertyNames() {
		for _, indexDef := range s.schema.Properties[prop].Indexes {
			if _, ok := s.indexes[indexDef.Name]; !ok {
				s.indexes[indexDef.Name] = map[string]map[IndexKey]map[string]struct{}{}
			}
			if _, ok := s.indexes[indexDef.Name][prop]; !ok {
				s.indexes[indexDef.Name][prop] = map[IndexKey]map[string]struct{}{}
			}
		}
	}
	s.clock = data.Clock
	for id, values := range data.Objects {
		obj := &objectState{values: make(map[string]*atomic.Value)}
		obj.exists.Store(true)
		s.objects[id] = obj
		s.objMu[id] = &sync.RWMutex{}
		for _, prop := range s.sortedPropertyNames() {
			s.propMu[s.propertyLockID(id, prop)] = &sync.RWMutex{}
			raw := Value{}
			if value, ok := values[prop]; ok {
				raw = Value{Present: value.Present, Data: value.Data}
			}
			slot := &atomic.Value{}
			slot.Store(raw)
			s.objects[id].values[prop] = slot
			for _, indexDef := range s.schema.Properties[prop].Indexes {
				s.addEntryLocked(indexDef.Name, prop, effectiveKey(indexDef, raw), id)
			}
		}
	}
	return nil
}

var errCrash = ErrSimulatedCrash

func recoverWAL(s *store, path string) (bool, walRecord, error) {
	if path == "" {
		return false, walRecord{}, nil
	}
	baseline := snapshotPath(path)
	if _, err := os.Stat(preSnapshotPath(path)); err == nil {
		baseline = preSnapshotPath(path)
	} else if !os.IsNotExist(err) {
		return false, walRecord{}, err
	}
	if err := loadSnapshot(s, baseline); err != nil {
		return false, walRecord{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, walRecord{}, nil
		}
		return false, walRecord{}, err
	}
	defer f.Close()

	last, err := readLastLine(f)
	if err != nil {
		return false, walRecord{}, err
	}
	if len(last) == 0 {
		return false, walRecord{}, nil
	}
	var rec walRecord
	if err := json.Unmarshal(last, &rec); err != nil {
		return false, walRecord{}, err
	}
	if rec.State == "commit" {
		if err := os.Remove(preSnapshotPath(path)); err != nil && !os.IsNotExist(err) {
			return false, rec, err
		}
		return false, rec, nil
	}
	undoTransaction(s, rec)
	if err := os.Rename(preSnapshotPath(path), snapshotPath(path)); err != nil && !os.IsNotExist(err) {
		return true, rec, err
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return true, rec, err
	}
	return true, rec, nil
}

func readLastLine(f *os.File) ([]byte, error) {
	var last []byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) > 0 {
			last = append(last[:0], line...)
		}
	}
	return last, scanner.Err()
}

func undoTransaction(s *store, rec walRecord) {
	for _, item := range rec.Items {
		obj := s.objects[item.Object]
		if obj == nil {
			continue
		}
		for _, membership := range item.Memberships {
			s.removeEntryLocked(membership.Index, membership.Property, membership.NewKey, item.Object)
			if membership.OldExist {
				s.addEntryLocked(membership.Index, membership.Property, membership.OldKey, item.Object)
			}
		}
		if item.Property != "" {
			obj.values[item.Property].Store(item.OldValue)
		}
		obj.exists.Store(item.OldExists)
	}
}

func ensureWALRecord(rec walRecord) error {
	if len(rec.Items) == 0 {
		return fmt.Errorf("empty transaction")
	}
	return nil
}
