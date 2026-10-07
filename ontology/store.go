package ontology

import "sync"

// Store is the underlying object/link/property storage. It deliberately exposes
// only the primitives the aggregation subsystem needs; the engine keeps all
// derived aggregate state separately and is the sole writer of that state.
type Store struct {
	mu sync.RWMutex

	objects map[string]map[string]objRecord // type -> id -> record
	links   map[string]map[[2]string]bool   // linkType -> {aggID, groupID}

	// memberVersion[(linkType, aggID)] is the optimistic-concurrency token for
	// that instance's membership set. It is bumped on every create/delete of a
	// qualifying link touching the instance.
	memberVersion map[string]int64
}

type objRecord struct {
	typ  string
	prop map[string]Optional
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		objects:       map[string]map[string]objRecord{},
		links:         map[string]map[[2]string]bool{},
		memberVersion: map[string]int64{},
	}
}

func (s *Store) objectLocked(typ, id string) (objRecord, bool) {
	bucket := s.objects[typ]
	if bucket == nil {
		return objRecord{}, false
	}
	rec, ok := bucket[id]
	return rec, ok
}

func versionKey(linkType, aggID string) string { return linkType + "\x00" + aggID }

func (s *Store) bumpVersionLocked(linkType, aggID string) int64 {
	key := versionKey(linkType, aggID)
	s.memberVersion[key]++
	return s.memberVersion[key]
}

func (s *Store) linkSetLocked(linkType string) map[[2]string]bool {
	ls := s.links[linkType]
	if ls == nil {
		ls = map[[2]string]bool{}
		s.links[linkType] = ls
	}
	return ls
}

func (s *Store) CreateObject(typ, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.objects[typ]
	if bucket == nil {
		bucket = map[string]objRecord{}
		s.objects[typ] = bucket
	}
	if _, ok := bucket[id]; ok {
		return classified(ClassInvalid, "createObject", "object already exists: "+id)
	}
	bucket[id] = objRecord{typ: typ, prop: map[string]Optional{}}
	return nil
}

func (s *Store) ObjectExists(typ, id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objectLocked(typ, id)
	return ok
}

// ObjectIDs enumerates all existing instance IDs of a type. It is used by the
// independent naive recomputation model (full scans are the reference model's
// job, never the incremental engine's).
func (s *Store) ObjectIDs(typ string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.objects[typ]
	out := make([]string, 0, len(bucket))
	for id := range bucket {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

// SetProperty writes a property. An Optional with Present==false clears the
// property to the absent state.
func (s *Store) SetProperty(typ, id, prop string, v Optional) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.objectLocked(typ, id)
	if !ok {
		return classified(ClassInvalid, "setProperty", "object does not exist: "+id)
	}
	rec.prop[prop] = v
	s.objects[typ][id] = rec
	return nil
}

func (s *Store) GetProperty(typ, id, prop string) (Optional, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.objectLocked(typ, id)
	if !ok {
		return Optional{}, false
	}
	v, ok := rec.prop[prop]
	return v, ok
}

// DeleteObject removes the object record. Links must be detached separately by
// the caller (DeleteMember/DeleteGroup) so the engine can observe the
// membership set first.
func (s *Store) DeleteObject(typ, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.objects[typ]
	if bucket == nil {
		return false
	}
	if _, ok := bucket[id]; !ok {
		return false
	}
	delete(bucket, id)
	return true
}

// AddLink creates a link (aggID -> groupID), reporting whether it was new and
// the membership-set version after the operation.
func (s *Store) AddLink(linkType, aggID, groupID string) (bool, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [2]string{aggID, groupID}
	ls := s.linkSetLocked(linkType)
	added := !ls[key]
	ls[key] = true
	return added, s.bumpVersionLocked(linkType, aggID)
}

// RemoveLink deletes a link, reporting whether it existed and the new version.
func (s *Store) RemoveLink(linkType, aggID, groupID string) (bool, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [2]string{aggID, groupID}
	ls := s.links[linkType]
	removed := ls != nil && ls[key]
	if removed {
		delete(ls, key)
	}
	return removed, s.bumpVersionLocked(linkType, aggID)
}

func (s *Store) LinkExists(linkType, aggID, groupID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ls := s.links[linkType]
	return ls != nil && ls[[2]string{aggID, groupID}]
}

// Memberships returns the sorted group IDs the instance currently belongs to.
func (s *Store) Memberships(linkType, aggID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k := range s.links[linkType] {
		if k[0] == aggID {
			out = append(out, k[1])
		}
	}
	sortStrings(out)
	return out
}

// RemoveAllLinksOfAgg detaches one instance from every group.
func (s *Store) RemoveAllLinksOfAgg(linkType, aggID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls := s.links[linkType]
	var groups []string
	if ls != nil {
		for k := range ls {
			if k[0] == aggID {
				groups = append(groups, k[1])
				delete(ls, k)
			}
		}
	}
	if len(groups) > 0 {
		s.bumpVersionLocked(linkType, aggID)
	}
	sortStrings(groups)
	return groups
}

// RemoveAllLinksToGroup detaches every aggregated instance from a deleted
// group, returning the affected aggregated instance IDs.
func (s *Store) RemoveAllLinksToGroup(linkType, groupID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls := s.links[linkType]
	var aggs []string
	if ls != nil {
		for k := range ls {
			if k[1] == groupID {
				aggs = append(aggs, k[0])
				delete(ls, k)
			}
		}
	}
	for _, aggID := range aggs {
		s.bumpVersionLocked(linkType, aggID)
	}
	sortStrings(aggs)
	return aggs
}

func (s *Store) MembersVersion(linkType, aggID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memberVersion[versionKey(linkType, aggID)]
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// storeSnap is a deep snapshot used to roll a processing unit back atomically.
type storeSnap struct {
	objects       map[string]map[string]objRecord
	links         map[string]map[[2]string]bool
	memberVersion map[string]int64
}

func (s *Store) snapshot() storeSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	objs := make(map[string]map[string]objRecord, len(s.objects))
	for typ, bucket := range s.objects {
		nb := make(map[string]objRecord, len(bucket))
		for id, rec := range bucket {
			np := make(map[string]Optional, len(rec.prop))
			for k, v := range rec.prop {
				np[k] = v
			}
			nb[id] = objRecord{typ: rec.typ, prop: np}
		}
		objs[typ] = nb
	}
	links := make(map[string]map[[2]string]bool, len(s.links))
	for lt, set := range s.links {
		ns := make(map[[2]string]bool, len(set))
		for k, v := range set {
			ns[k] = v
		}
		links[lt] = ns
	}
	vers := make(map[string]int64, len(s.memberVersion))
	for k, v := range s.memberVersion {
		vers[k] = v
	}
	return storeSnap{objects: objs, links: links, memberVersion: vers}
}

func (s *Store) restore(snap storeSnap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = snap.objects
	s.links = snap.links
	s.memberVersion = snap.memberVersion
}
