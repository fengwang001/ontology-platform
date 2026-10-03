package objstore

import (
	"sync"

	"ontology/cond"
)

// vrec is one version of a key in the per-key doubly linked list ordered by
// version number. The list head is the current version (largest number).
type vrec struct {
	number int64
	size   int64
	etag   string
	mtime  int64
	marker bool
	prev   *vrec
	next   *vrec
}

type keyState struct {
	byNum map[int64]*vrec
	head  *vrec
	tail  *vrec
}

type tenantState struct {
	versioned bool
	seq       int64
	keys      map[string]*keyState
}

// Store holds every tenant's objects and version sequences. Per-tenant locks
// (not one global lock) serialize condition evaluation against install.
type Store struct {
	mu      sync.Mutex
	tenants map[string]*tenantState
	locks   sync.Map // map[string]*sync.Mutex

	// touched counts version records inspected by the most recent
	// current-version decision. Decisions start from the linked-list head and
	// never walk the version history, so the bound is <= 2 regardless of how
	// many versions the key has.
	touchedMu sync.Mutex
	touched   int
}

func NewStore() *Store {
	return &Store{tenants: make(map[string]*tenantState)}
}

func (s *Store) tenantLock(tenant string) *sync.Mutex {
	lock, _ := s.locks.LoadOrStore(tenant, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *Store) Lock(tenant string)   { s.tenantLock(tenant).Lock() }
func (s *Store) Unlock(tenant string) { s.tenantLock(tenant).Unlock() }

func (s *Store) get(tenant string) *tenantState {
	t := s.tenants[tenant]
	if t == nil {
		t = &tenantState{keys: make(map[string]*keyState)}
		s.tenants[tenant] = t
	}
	return t
}

// Versioning reports whether the tenant bucket is versioned.
func (s *Store) Versioning(tenant string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(tenant).versioned
}

// EnableVersioning performs the irreversible off -> on transition. Enabling an
// already-versioned bucket is idempotent; disabling is rejected by the caller.
func (s *Store) EnableVersioning(tenant string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.get(tenant).versioned = true
}

func (ks *keyState) add(r *vrec) {
	ks.byNum[r.number] = r
	if ks.head == nil {
		ks.head = r
		ks.tail = r
		return
	}
	r.prev = ks.head
	ks.head.next = r
	ks.head = r
}

func (ks *keyState) remove(r *vrec) {
	delete(ks.byNum, r.number)
	if r.prev != nil {
		r.prev.next = r.next
	} else {
		ks.tail = r.next
	}
	if r.next != nil {
		r.next.prev = r.prev
	} else {
		ks.head = r.prev
	}
}

// View returns the current-version view of a key. The decision starts a fresh
// touch counter and reads only the list head, so it inspects at most 2
// records regardless of the key's version count.
func (s *Store) View(tenant, key string) cond.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.get(tenant)
	ks := t.keys[key]
	v := cond.View{}
	if ks == nil || ks.head == nil {
		return v
	}
	s.beginDecision()
	s.touch(1)
	r := ks.head
	v.Exists = true
	v.Marker = r.marker
	v.Etag = r.etag
	v.Mtime = r.mtime
	return v
}

// VersionSize reports a specific version's size and existence. Markers exist
// but have size 0.
func (s *Store) VersionSize(tenant, key string, ver int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.get(tenant).keys[key]
	if ks == nil {
		return 0, false
	}
	r, ok := ks.byNum[ver]
	if !ok {
		return 0, false
	}
	return r.size, true
}

// InstallData appends a data version. With versioning off it replaces the
// unique version-0 object; oldSize is its released size (0 when absent). With
// versioning on a new monotonically numbered version is allocated.
func (s *Store) InstallData(tenant, key string, size int64, etag string, now int64) (ver, oldSize int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.get(tenant)
	ks := t.keys[key]
	if ks == nil {
		ks = &keyState{byNum: make(map[int64]*vrec)}
		t.keys[key] = ks
	}
	if !t.versioned {
		if ks.tail != nil {
			old := ks.tail
			oldSize = old.size
			ks.remove(old)
		}
		r := &vrec{number: 0, size: size, etag: etag, mtime: now}
		ks.add(r)
		return 0, oldSize
	}
	t.seq++
	r := &vrec{number: t.seq, size: size, etag: etag, mtime: now}
	ks.add(r)
	return t.seq, 0
}

// InstallMarker appends a delete marker as the next version (even when the key
// has no versions at all) and returns its version number. Versioning on only.
func (s *Store) InstallMarker(tenant, key string, now int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.get(tenant)
	ks := t.keys[key]
	if ks == nil {
		ks = &keyState{byNum: make(map[int64]*vrec)}
		t.keys[key] = ks
	}
	t.seq++
	r := &vrec{number: t.seq, marker: true, mtime: now}
	ks.add(r)
	return t.seq
}

// RemoveVersion permanently deletes the given version (data record or marker,
// including version 0). Its size (0 for markers) is reported.
func (s *Store) RemoveVersion(tenant, key string, ver int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.get(tenant).keys[key]
	if ks == nil {
		return 0, false
	}
	r, ok := ks.byNum[ver]
	if !ok {
		return 0, false
	}
	size := r.size
	ks.remove(r)
	return size, true
}

// RemoveUnique deletes the sole version-0 object with versioning off.
func (s *Store) RemoveUnique(tenant, key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.get(tenant).keys[key]
	if ks == nil || ks.tail == nil {
		return 0, false
	}
	r := ks.tail
	size := r.size
	ks.remove(r)
	return size, true
}

// CurrentSize returns the size of the key's current version and whether a data
// object is current. A missing key or a current marker yields (0, false).
func (s *Store) CurrentSize(tenant, key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.get(tenant).keys[key]
	if ks == nil || ks.head == nil || ks.head.marker {
		return 0, false
	}
	s.beginDecision()
	s.touch(1)
	return ks.head.size, true
}

// DecisionTouched returns records inspected by the most recent current-version
// decision. It is bounded by 2 independently of the key's version count.
func (s *Store) DecisionTouched() int {
	s.touchedMu.Lock()
	defer s.touchedMu.Unlock()
	return s.touched
}

func (s *Store) beginDecision() {
	s.touchedMu.Lock()
	s.touched = 0
	s.touchedMu.Unlock()
}

func (s *Store) touch(n int) {
	s.touchedMu.Lock()
	s.touched += n
	s.touchedMu.Unlock()
}
