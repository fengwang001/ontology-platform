package ontology

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrFileNotFound    = errors.New("file not found")
	ErrNoAttribute     = errors.New("attribute not found")
	ErrAttributeTooBig = errors.New("attribute placement is invalid")
	ErrPoolFull        = errors.New("block pool is full")
)

type Mode uint8

const (
	Inline Mode = iota
	Block
)

type Location uint8

const (
	InInode Location = iota
	External
)

type Xattr struct {
	Name  string
	Value []byte
}

type XattrLocation struct {
	Name     string
	Location Location
}

type FileInfo struct {
	Mode          Mode
	Size          int64
	DataBlocks    int64
	ExtID         int
	ExternalBytes int64
	Xattrs        []XattrLocation
}

type fileEntry struct {
	mode  Mode
	size  int64
	attrs map[string][]byte
}

type layout struct {
	inline        map[string]bool
	externalKey   string
	externalBytes int64
	names         []string
}

type Manager struct {
	mu sync.RWMutex

	a  int64
	x  int64
	bs int64
	p  int64

	nextID int
	files  map[int]*fileEntry
	used   int64
}

func NewManager(a, x, blockSize, poolBlocks int64) (*Manager, error) {
	if a < 1 || x < 1 || blockSize < 1 || poolBlocks < 1 {
		return nil, ErrInvalidArgument
	}
	return &Manager{
		a:      a,
		x:      x,
		bs:     blockSize,
		p:      poolBlocks,
		nextID: 1,
		files:  make(map[int]*fileEntry),
	}, nil
}

func (m *Manager) Create() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.files[id] = &fileEntry{attrs: make(map[string][]byte)}
	m.nextID++
	return id, nil
}

func (m *Manager) Resize(fileID int, size int64) error {
	if size < 0 || size > 1<<40 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file := m.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}

	desired := file.mode
	if file.mode == Inline {
		if size <= m.a {
			if _, ok := m.computeLayout(Inline, size, file.attrs); ok {
				desired = Inline
			} else {
				desired = Block
			}
		} else {
			desired = Block
		}
	} else if size == 0 {
		desired = Inline
	}

	if _, ok := m.computeLayout(desired, size, file.attrs); !ok {
		return ErrAttributeTooBig
	}

	snapshot := *file
	file.mode = desired
	file.size = size
	return m.commitOrRollback(file, snapshot)
}

func (m *Manager) SetXattr(fileID int, name string, value []byte) error {
	if len(name) < 1 || len(name) > 255 || len(value) > 4096 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file := m.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}

	candidate := cloneAttrs(file.attrs)
	candidate[name] = append([]byte(nil), value...)

	desired := file.mode
	if file.mode == Inline {
		if _, ok := m.computeLayout(Inline, file.size, candidate); ok {
			desired = Inline
		} else if _, ok = m.computeLayout(Block, file.size, candidate); ok {
			desired = Block
		} else {
			return ErrAttributeTooBig
		}
	} else if _, ok := m.computeLayout(Block, file.size, candidate); !ok {
		return ErrAttributeTooBig
	}

	snapshot := *file
	file.mode = desired
	file.attrs = candidate
	return m.commitOrRollback(file, snapshot)
}

func (m *Manager) RemoveXattr(fileID int, name string) error {
	if len(name) < 1 || len(name) > 255 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file := m.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}
	if _, ok := file.attrs[name]; !ok {
		return ErrNoAttribute
	}

	candidate := cloneAttrs(file.attrs)
	delete(candidate, name)

	desired := file.mode
	if file.mode == Inline {
		if _, ok := m.computeLayout(Inline, file.size, candidate); !ok {
			return ErrAttributeTooBig
		}
	} else if _, ok := m.computeLayout(Block, file.size, candidate); !ok {
		return ErrAttributeTooBig
	}

	snapshot := *file
	file.mode = desired
	file.attrs = candidate
	return m.commitOrRollback(file, snapshot)
}

func (m *Manager) GetXattr(fileID int, name string) ([]byte, error) {
	if len(name) < 1 || len(name) > 255 {
		return nil, ErrInvalidArgument
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	file := m.files[fileID]
	if file == nil {
		return nil, ErrFileNotFound
	}
	value, ok := file.attrs[name]
	if !ok {
		return nil, ErrNoAttribute
	}
	return append([]byte(nil), value...), nil
}

func (m *Manager) Clone(fileID int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	file := m.files[fileID]
	if file == nil {
		return 0, ErrFileNotFound
	}

	id := m.nextID
	clone := &fileEntry{
		mode:  file.mode,
		size:  file.size,
		attrs: cloneAttrs(file.attrs),
	}
	m.files[id] = clone

	used := m.recomputeUsed()
	if used > m.p {
		delete(m.files, id)
		return 0, ErrPoolFull
	}

	m.used = used
	m.nextID++
	return id, nil
}

func (m *Manager) Stat(fileID int) (FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	file := m.files[fileID]
	if file == nil {
		return FileInfo{}, ErrFileNotFound
	}

	placement, _ := m.computeLayout(file.mode, file.size, file.attrs)
	info := FileInfo{
		Mode:          file.mode,
		Size:          file.size,
		ExternalBytes: placement.externalBytes,
		Xattrs:        make([]XattrLocation, 0, len(placement.names)),
	}
	if file.mode == Block {
		info.DataBlocks = dataBlocks(file.size, m.bs)
	}
	if placement.externalKey != "" {
		info.ExtID = m.extIDLocked(placement.externalKey)
	}
	for _, name := range placement.names {
		location := External
		if placement.inline[name] {
			location = InInode
		}
		info.Xattrs = append(info.Xattrs, XattrLocation{Name: name, Location: location})
	}
	return info, nil
}

func (m *Manager) UsedBlocks() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.used
}

func (m *Manager) commitOrRollback(file *fileEntry, snapshot fileEntry) error {
	used := m.recomputeUsed()
	if used > m.p {
		*file = snapshot
		m.used = m.recomputeUsed()
		return ErrPoolFull
	}
	m.used = used
	return nil
}

func (m *Manager) recomputeUsed() int64 {
	external := make(map[string]struct{})
	var total int64
	for _, file := range m.files {
		if file.mode == Block {
			total += dataBlocks(file.size, m.bs)
		}
		placement, ok := m.computeLayout(file.mode, file.size, file.attrs)
		if !ok || placement.externalKey == "" {
			continue
		}
		external[placement.externalKey] = struct{}{}
	}
	return total + int64(len(external))
}

func (m *Manager) extIDLocked(key string) int {
	id := 0
	for fileID, file := range m.files {
		placement, ok := m.computeLayout(file.mode, file.size, file.attrs)
		if ok && placement.externalKey == key && (id == 0 || fileID < id) {
			id = fileID
		}
	}
	return id
}

func (m *Manager) computeLayout(mode Mode, size int64, attrs map[string][]byte) (layout, bool) {
	if size < 0 || (mode == Inline && size > m.a) {
		return layout{}, false
	}

	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)

	result := layout{
		inline: make(map[string]bool, len(attrs)),
		names:  names,
	}
	remaining := m.a
	if mode == Block {
		remaining = m.a
	} else {
		remaining = m.a - size
	}

	var external bytes.Buffer
	usingExternal := false
	for _, name := range names {
		value := attrs[name]
		needed := int64(4 + roundUp4(len(name)) + roundUp4(len(value)))
		if !usingExternal && needed <= remaining {
			remaining -= needed
			result.inline[name] = true
			continue
		}

		usingExternal = true
		result.externalBytes += needed
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(name)))
		external.Write(header[:])
		external.WriteString(name)
		binary.BigEndian.PutUint32(header[:], uint32(len(value)))
		external.Write(header[:])
		external.Write(value)
	}

	if result.externalBytes > m.x {
		return layout{}, false
	}
	if usingExternal {
		result.externalKey = external.String()
	}
	return result, true
}

func cloneAttrs(attrs map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(attrs))
	for name, value := range attrs {
		result[name] = append([]byte(nil), value...)
	}
	return result
}

func dataBlocks(size, blockSize int64) int64 {
	if size == 0 {
		return 0
	}
	return (size + blockSize - 1) / blockSize
}

func roundUp4(n int) int {
	return (n + 3) &^ 3
}
