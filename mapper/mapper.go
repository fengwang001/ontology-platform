package mapper

import (
	"strings"
	"sync"
)

// Config configures a Mapper.
type Config struct {
	MaxBytes   int
	MaxPath    int
	MaxEntries int
}

// Mapper maps source names to target-safe names.
type Mapper struct {
	mu      sync.RWMutex
	cfg     Config
	nextID  int
	entries map[int]*dentry
}

type dentry struct {
	id     int
	parent int
	src    string
	mapped string
	dir    bool
	// Directories only:
	bySrc map[string]*dentry // source name (case-sensitive) -> child
	used  map[string]string  // folded mapped key -> mapped name
}

func newDir(id, parent int) *dentry {
	return &dentry{
		id:     id,
		parent: parent,
		dir:    true,
		bySrc:  map[string]*dentry{},
		used:   map[string]string{},
	}
}

// New returns an empty Mapper holding the root directory (id 0).
func New(cfg Config) *Mapper {
	if cfg.MaxBytes < 16 || cfg.MaxBytes > 255 ||
		cfg.MaxPath < cfg.MaxBytes || cfg.MaxPath > 4096 ||
		cfg.MaxEntries < 1 || cfg.MaxEntries > 100000 {
		panic("mapper: invalid config")
	}
	root := newDir(0, -1)
	return &Mapper{
		cfg:     cfg,
		nextID:  1,
		entries: map[int]*dentry{0: root},
	}
}

// Add creates a new entry and allocates a target name in parent.
func (m *Mapper) Add(parent int, src string, isDir bool) (id int, err error) {
	if !validSrc(src) {
		return 0, ErrInvalidName
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.entries[parent]
	if !ok || !p.dir {
		return 0, ErrNoParent
	}
	if _, ok := p.bySrc[src]; ok {
		return 0, ErrExists
	}
	if len(p.bySrc) >= m.cfg.MaxEntries {
		return 0, ErrFull
	}
	t0 := baseMap(src, m.cfg.MaxBytes)
	name, err := uniqueName(p, t0, m.cfg.MaxBytes)
	if err != nil {
		return 0, err
	}
	e := &dentry{
		id:     m.nextID,
		parent: parent,
		src:    src,
		mapped: name,
		dir:    isDir,
	}
	if isDir {
		e.bySrc = map[string]*dentry{}
		e.used = map[string]string{}
	}
	if m.pathLen(e) > m.cfg.MaxPath {
		return 0, ErrPathTooLong
	}
	p.bySrc[src] = e
	p.used[fold(name)] = name
	m.entries[e.id] = e
	m.nextID++
	return e.id, nil
}

// Remove deletes the entry identified by src in parent.
func (m *Mapper) Remove(parent int, src string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.entries[parent]
	if !ok || !p.dir {
		return ErrNotFound
	}
	e, ok := p.bySrc[src]
	if !ok {
		return ErrNotFound
	}
	if e.dir && len(e.bySrc) > 0 {
		return ErrNotEmpty
	}
	delete(p.bySrc, src)
	delete(p.used, fold(e.mapped))
	delete(m.entries, e.id)
	return nil
}

// Rename changes the source name of an entry, keeping its id.
func (m *Mapper) Rename(parent int, src, newSrc string) error {
	if !validSrc(newSrc) {
		return ErrInvalidName
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.entries[parent]
	if !ok || !p.dir {
		return ErrNotFound
	}
	e, ok := p.bySrc[src]
	if !ok {
		return ErrNotFound
	}
	if newSrc == src {
		return nil
	}
	if _, ok := p.bySrc[newSrc]; ok {
		return ErrExists
	}

	oldName := e.mapped

	// Release first, so the entry's own folded key is not seen as taken.
	delete(p.bySrc, src)
	delete(p.used, fold(oldName))

	t0 := baseMap(newSrc, m.cfg.MaxBytes)
	name, err := uniqueName(p, t0, m.cfg.MaxBytes)
	if err != nil {
		m.rollbackRename(p, e, src, oldName)
		return err
	}

	// Path-length check: new entry and, for directories, the whole subtree.
	e.src = newSrc
	e.mapped = name
	if m.subtreeTooLong(e) {
		m.rollbackRename(p, e, src, oldName)
		return ErrPathTooLong
	}

	p.bySrc[newSrc] = e
	p.used[fold(name)] = name
	return nil
}

func (m *Mapper) rollbackRename(p, e *dentry, src, oldName string) {
	e.src = src
	e.mapped = oldName
	p.bySrc[src] = e
	p.used[fold(oldName)] = oldName
}

// subtreeTooLong reports whether e or any descendant exceeds MaxPath.
func (m *Mapper) subtreeTooLong(e *dentry) bool {
	if m.pathLen(e) > m.cfg.MaxPath {
		return true
	}
	if e.dir {
		for _, ch := range e.bySrc {
			if m.subtreeTooLong(ch) {
				return true
			}
		}
	}
	return false
}

// pathLen returns the mapped path length of e, walking ancestors.
func (m *Mapper) pathLen(e *dentry) int {
	total := 0
	for cur := e; cur.id != 0; cur = m.entries[cur.parent] {
		total += len(cur.mapped) + 1
	}
	return total - 1
}

// Lookup returns the id of src within parent.
func (m *Mapper) Lookup(parent int, src string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.entries[parent]
	if !ok || !p.dir {
		return 0, ErrNotFound
	}
	e, ok := p.bySrc[src]
	if !ok {
		return 0, ErrNotFound
	}
	return e.id, nil
}

// Path returns the mapped absolute path of id. The root returns "".
func (m *Mapper) Path(id int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[id]
	if !ok {
		return "", ErrNotFound
	}
	if id == 0 {
		return "", nil
	}
	var parts []string
	for cur := e; cur.id != 0; cur = m.entries[cur.parent] {
		parts = append(parts, cur.mapped)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/"), nil
}

// Names returns the mapped names of parent's entries in byte order.
func (m *Mapper) Names(parent int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.entries[parent]
	if !ok || !d.dir {
		return nil, ErrNotFound
	}
	return sortedNames(d), nil
}

// Errors reported by Mapper methods.
var (
	ErrInvalidName = errMapper("mapper: invalid name")
	ErrNoParent    = errMapper("mapper: parent does not exist or is not a directory")
	ErrExists      = errMapper("mapper: source name already exists")
	ErrNotFound    = errMapper("mapper: entry not found")
	ErrFull        = errMapper("mapper: directory is full")
	ErrCannotFit   = errMapper("mapper: name cannot fit within MaxBytes")
	ErrPathTooLong = errMapper("mapper: mapped path exceeds MaxPath")
	ErrNotEmpty    = errMapper("mapper: directory is not empty")
)

type errMapper string

func (e errMapper) Error() string { return string(e) }
