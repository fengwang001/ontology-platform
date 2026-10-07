package ontology

import (
	"sort"
	"sync"
	"sync/atomic"
)

type store struct {
	mu      sync.RWMutex
	schema  Schema
	objects map[string]*objectState
	propMu  map[string]*sync.RWMutex
	objMu   map[string]*sync.RWMutex
	indexMu map[string]*sync.RWMutex
	indexes map[string]map[string]map[IndexKey]map[string]struct{}
	clock   uint64
	crashed bool
}

type objectState struct {
	exists atomic.Bool
	values map[string]*atomic.Value
}

func newStore(schema Schema) *store {
	s := &store{
		schema:  cloneSchema(schema),
		objects: make(map[string]*objectState),
		propMu:  make(map[string]*sync.RWMutex),
		objMu:   make(map[string]*sync.RWMutex),
		indexMu: make(map[string]*sync.RWMutex),
		indexes: make(map[string]map[string]map[IndexKey]map[string]struct{}),
	}
	for propName, prop := range s.schema.Properties {
		for _, index := range prop.Indexes {
			if _, ok := s.indexes[index.Name]; !ok {
				s.indexes[index.Name] = make(map[string]map[IndexKey]map[string]struct{})
				s.indexMu[index.Name] = &sync.RWMutex{}
			}
			if _, ok := s.indexes[index.Name][propName]; !ok {
				s.indexes[index.Name][propName] = make(map[IndexKey]map[string]struct{})
			}
		}
	}
	return s
}

func cloneSchema(in Schema) Schema {
	out := Schema{Properties: make(map[string]PropertyDef, len(in.Properties))}
	for name, prop := range in.Properties {
		out.Properties[name] = prop
	}
	return out
}

func (s *store) CreateObject(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[id]; exists {
		return false
	}
	obj := &objectState{values: make(map[string]*atomic.Value)}
	obj.exists.Store(true)
	for propName := range s.schema.Properties {
		slot := &atomic.Value{}
		slot.Store(Missing())
		obj.values[propName] = slot
	}
	s.objects[id] = obj
	s.objMu[id] = &sync.RWMutex{}
	for propName := range s.schema.Properties {
		s.propMu[s.propertyLockID(id, propName)] = &sync.RWMutex{}
	}
	for propName, prop := range s.schema.Properties {
		for _, index := range prop.Indexes {
			s.addEntryLocked(index.Name, propName, MissingKey(), id)
		}
	}
	return true
}

func (s *store) propertyLockID(object, property string) string {
	return object + "\x00" + property
}

func (s *store) lockObject(id string) *sync.RWMutex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.objMu[id]
}

func (s *store) lockProperty(object, property string) *sync.RWMutex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.propMu[s.propertyLockID(object, property)]
}

func (s *store) lockIndex(name string) *sync.RWMutex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexMu[name]
}

func (s *store) sortedPropertyNames() []string {
	names := make([]string, 0, len(s.schema.Properties))
	for name := range s.schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *store) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj := s.objects[id]
	return obj != nil && obj.exists.Load()
}

func (s *store) GetLocked(id string, property string) (Value, bool) {
	obj := s.objects[id]
	if obj == nil || !obj.exists.Load() {
		return Value{}, false
	}
	return obj.values[property].Load().(Value), true
}

func (s *store) SetValueLocked(id string, property string, value Value) {
	s.objects[id].values[property].Store(value)
}

func (s *store) allValuesLocked(id string) map[string]Value {
	values := make(map[string]Value)
	for name, slot := range s.objects[id].values {
		values[name] = slot.Load().(Value)
	}
	return values
}

func (s *store) setDeletedLocked(id string) {
	s.objects[id].exists.Store(false)
}

func (s *store) IndexQuery(indexName, property string, key IndexKey) []string {
	indexLock := s.lockIndex(indexName)
	indexLock.RLock()
	candidates := make([]string, 0)
	for id := range s.indexes[indexName][property][key] {
		candidates = append(candidates, id)
	}
	indexLock.RUnlock()

	objectLocks := make([]*sync.RWMutex, 0, len(candidates))
	propertyLocks := make([]*sync.RWMutex, 0, len(candidates))
	for _, id := range candidates {
		if objectLock := s.lockObject(id); objectLock != nil {
			objectLock.RLock()
			objectLocks = append(objectLocks, objectLock)
		}
		if propertyLock := s.lockProperty(id, property); propertyLock != nil {
			propertyLock.RLock()
			propertyLocks = append(propertyLocks, propertyLock)
		}
	}
	defer func() {
		for _, lock := range propertyLocks {
			lock.RUnlock()
		}
		for _, lock := range objectLocks {
			lock.RUnlock()
		}
	}()

	indexLock.RLock()
	defer indexLock.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(candidates))
	for _, id := range candidates {
		obj := s.objects[id]
		if obj == nil || !obj.exists.Load() {
			continue
		}
		if _, ok := s.indexes[indexName][property][key][id]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func MissingKey() IndexKey { return IndexKey{} }

func (s *store) addEntryLocked(indexName, property string, key IndexKey, object string) bool {
	byKey := s.indexes[indexName][property]
	byObject := byKey[key]
	if byObject == nil {
		byObject = make(map[string]struct{})
		byKey[key] = byObject
	}
	if _, exists := byObject[object]; exists {
		return false
	}
	byObject[object] = struct{}{}
	return true
}

func (s *store) removeEntryLocked(indexName, property string, key IndexKey, object string) bool {
	byObject := s.indexes[indexName][property][key]
	if _, exists := byObject[object]; !exists {
		return false
	}
	delete(byObject, object)
	if len(byObject) == 0 {
		delete(s.indexes[indexName][property], key)
	}
	return true
}
