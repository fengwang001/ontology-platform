package ontology

import (
	"fmt"
	"sort"
	"sync"
)

func appendChange(changes map[string]EntryChange, entry IndexEntry, added bool) {
	key := entry.Index + "\x00" + entry.Property
	change := changes[key]
	if added {
		change.Added = append(change.Added, entry)
	} else {
		change.Removed = append(change.Removed, entry)
	}
	changes[key] = change
}

func cloneChanges(in map[string]EntryChange) map[string]EntryChange {
	out := make(map[string]EntryChange, len(in))
	for key, change := range in {
		copied := EntryChange{}
		copied.Added = append(copied.Added, change.Added...)
		copied.Removed = append(copied.Removed, change.Removed...)
		out[key] = copied
	}
	return out
}

func indexEntry(indexName, property string, key IndexKey, object string) IndexEntry {
	return IndexEntry{Index: indexName, Property: property, Key: key, Object: object}
}

func indexDefNames(defs []IndexDef) []string {
	names := make([]string, 0, len(defs))
	seen := map[string]struct{}{}
	for _, def := range defs {
		if _, ok := seen[def.Name]; ok {
			continue
		}
		seen[def.Name] = struct{}{}
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}

func indexDefExists(defs []IndexDef, name string) bool {
	for _, def := range defs {
		if def.Name == name {
			return true
		}
	}
	return false
}

func lockIndexes(s *store, names []string) []*sync.RWMutex {
	locks := make([]*sync.RWMutex, 0, len(names))
	for _, name := range names {
		if lock := s.lockIndex(name); lock != nil {
			lock.Lock()
			locks = append(locks, lock)
		}
	}
	return locks
}

func unlockIndexes(locks []*sync.RWMutex) {
	for _, lock := range locks {
		lock.Unlock()
	}
}

func unlockAll(indexLocks, propertyLocks, objectLocks []*sync.RWMutex, objectRead bool) {
	unlockIndexes(indexLocks)
	for _, lock := range propertyLocks {
		lock.Unlock()
	}
	for _, lock := range objectLocks {
		if objectRead {
			lock.RUnlock()
		} else {
			lock.Unlock()
		}
	}
}

func (p *Platform) indexNamesFor(properties []string) []string {
	names := make([]string, 0)
	seen := map[string]struct{}{}
	for _, property := range properties {
		for _, index := range p.store.schema.Properties[property].Indexes {
			if _, ok := seen[index.Name]; ok {
				continue
			}
			seen[index.Name] = struct{}{}
			names = append(names, index.Name)
		}
	}
	sort.Strings(names)
	return names
}

func changesFromRecord(rec walRecord) map[string]EntryChange {
	changes := map[string]EntryChange{}
	for _, item := range rec.Items {
		for _, membership := range item.Memberships {
			if membership.OldKey != membership.NewKey {
				if membership.OldExist {
					appendChange(changes, indexEntry(membership.Index, membership.Property, membership.OldKey, item.Object), false)
				}
				appendChange(changes, indexEntry(membership.Index, membership.Property, membership.NewKey, item.Object), true)
			}
		}
	}
	return changes
}

func (e *WriteError) message() string {
	if e.Reason != nil {
		return fmt.Sprintf("%s: %s", e.Code.String(), e.Reason.Error())
	}
	return e.Code.String()
}

func (c ErrorCode) String() string {
	switch c {
	case ErrObjectNotFound:
		return "object instance not found"
	case ErrPropertyNotIndexed:
		return "property does not support indexing"
	case ErrIndexMaintenance:
		return "index maintenance failed"
	case ErrBatchRollback:
		return "batch write rolled back"
	default:
		return "unknown error"
	}
}
