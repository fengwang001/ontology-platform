package layout

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

// 拒绝原因：操作只返回下列哨兵错误之一（可能被包装），可用 errors.Is 区分。
var (
	ErrInvalidArgument = errors.New("layout: invalid argument")
	ErrNotFound        = errors.New("layout: file not found")
	ErrNoXattr         = errors.New("layout: xattr not found")
	ErrXattrTooLarge   = errors.New("layout: xattr cannot fit even entirely in external block")
	ErrPoolFull        = errors.New("layout: block pool exhausted")
)

const (
	maxSize     = int64(1) << 40
	maxNameLen  = 255
	maxValueLen = 4096
	xattrHeader = 4
)

// Mode 是文件的存放模式。
type Mode int

const (
	// Inline 表示文件数据与尽量多的属性共享 inode 内区域。
	Inline Mode = iota
	// Block 表示文件数据位于块池中的数据块，inode 内区域全部可用于属性。
	Block
)

// Location 是单个属性的落点。
type Location int

const (
	// InInode 表示属性放在 inode 内区域。
	InInode Location = iota
	// External 表示属性放在外部属性块。
	External
)

// Xattr 是一个名字/值属性对。
type Xattr struct {
	Name  []byte
	Value []byte
}

// XattrLoc 描述一个属性的名字与其落点（Stat 按名字升序返回）。
type XattrLoc struct {
	Name     []byte
	Location Location
}

// StatInfo 是 Stat 的结果快照。
type StatInfo struct {
	Mode     Mode
	Size     int64
	DataUsed int64 // 数据占用的数据块数
	ExtID    int   // 外部属性集编号：Ext 为空为 0，否则为同 Ext 全部文件中最小编号
	Xattrs   []XattrLoc
}

// Manager 是小文件内联 / 扩展属性外部块布局管理器。
// 全部方法可并发调用，其结果等价于某种串行执行顺序。
type Manager struct {
	A, X int64
	Bs   int64
	P    int64

	mu    sync.Mutex
	files map[int]*fileState
	next  int
}

type fileState struct {
	mode Mode
	size int64
	// xattrs 始终按名字字节序排列。
	xattrs []Xattr
}

// placed 是一次纯函数式布局计算的结果。
type placed struct {
	locations []Location // 与输入属性一一对应
	extBytes  int64      // 外部放置的属性总字节
	extKey    []byte     // Ext 的逐字节序列化；为空表示 Ext 为空
	external  bool       // 是否存在外部属性
	dataUsed  int64
}

// New 创建管理器；A、X、Bs、P 均须不小于 1。
func New(A, X, Bs, P int64) *Manager {
	if A < 1 || X < 1 || Bs < 1 || P < 1 {
		panic(ErrInvalidArgument)
	}
	return &Manager{A: A, X: X, Bs: Bs, P: P, files: make(map[int]*fileState), next: 1}
}

func roundup4(n int) int { return (n + 3) &^ 3 }

func xattrSize(nameLen, valueLen int) int {
	return xattrHeader + roundup4(nameLen) + roundup4(valueLen)
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// place 按 (mode, size, 属性集) 计算布局。
// inode 内剩余空间：内联模式为 A-size，块模式为 A。
// 属性按名字升序依次尝试，占用不大于剩余即放入；第一个放不下的属性
// 及其后的全部属性一律外置。外部总字节不超过 X 才算合法。
func (m *Manager) place(mode Mode, size int64, xs []Xattr) (placed, bool) {
	var inodeLeft int64
	if mode == Inline {
		inodeLeft = m.A - size
	} else {
		inodeLeft = m.A
	}
	p := placed{locations: make([]Location, len(xs))}
	i := 0
	for ; i < len(xs); i++ {
		need := int64(xattrSize(len(xs[i].Name), len(xs[i].Value)))
		if need <= inodeLeft {
			p.locations[i] = InInode
			inodeLeft -= need
			continue
		}
		break
	}
	var extBuf bytes.Buffer
	for ; i < len(xs); i++ {
		p.locations[i] = External
		need := int64(xattrSize(len(xs[i].Name), len(xs[i].Value)))
		p.extBytes += need
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(xs[i].Name)))
		extBuf.Write(hdr[:])
		extBuf.Write(xs[i].Name)
		binary.BigEndian.PutUint32(hdr[:], uint32(len(xs[i].Value)))
		extBuf.Write(hdr[:])
		extBuf.Write(xs[i].Value)
	}
	p.external = p.extBytes > 0
	p.extKey = extBuf.Bytes()
	if mode == Block {
		p.dataUsed = ceilDiv(size, m.Bs)
	}
	return p, p.extBytes <= m.X
}

// poolUsed 按定义从头重算当前块池占用：
// 各文件数据占用块数之和 + 互不相同的非空 Ext 种数。
func (m *Manager) poolUsedLocked() int64 {
	var total int64
	exts := make(map[string]struct{})
	for _, f := range m.files {
		p, ok := m.place(f.mode, f.size, f.xattrs)
		if !ok {
			panic(ErrXattrTooLarge)
		}
		total += p.dataUsed
		if p.external {
			exts[string(p.extKey)] = struct{}{}
		}
	}
	return total + int64(len(exts))
}

// candidateUsed 计算把候选文件集合（含尚未提交的新建/克隆文件）
// 全部放入后的块池占用，按共享后的结果计算。
func (m *Manager) candidateUsed(candidates map[int]*fileState) int64 {
	var total int64
	exts := make(map[string]struct{})
	for id, f := range candidates {
		_ = id
		p, ok := m.place(f.mode, f.size, f.xattrs)
		if !ok {
			panic(ErrXattrTooLarge)
		}
		total += p.dataUsed
		if p.external {
			exts[string(p.extKey)] = struct{}{}
		}
	}
	return total + int64(len(exts))
}

func sortedXattrs(xs []Xattr) {
	sort.Slice(xs, func(i, j int) bool { return bytes.Compare(xs[i].Name, xs[j].Name) < 0 })
}

func cloneFile(f *fileState) *fileState {
	cp := &fileState{mode: f.mode, size: f.size, xattrs: make([]Xattr, len(f.xattrs))}
	for i, x := range f.xattrs {
		cp.xattrs[i] = Xattr{Name: append([]byte(nil), x.Name...), Value: append([]byte(nil), x.Value...)}
	}
	return cp
}

// commitLocked 在互斥锁内构造候选世界并检查块池，成功时替换全部文件状态。
// mutate 直接在 candidates（当前世界的深拷贝）上修改；返回 false 表示放置不合法。
func (m *Manager) commitLocked(mutate func(candidates map[int]*fileState) bool) error {
	candidates := make(map[int]*fileState, len(m.files)+1)
	for id, f := range m.files {
		candidates[id] = cloneFile(f)
	}
	if !mutate(candidates) {
		return ErrXattrTooLarge
	}
	used := m.candidateUsed(candidates)
	if used > m.P {
		return ErrPoolFull
	}
	m.files = candidates
	return nil
}

// Create 新建文件，返回文件编号（编号从 1 起连续）。
func (m *Manager) Create() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.next
	err := m.commitLocked(func(candidates map[int]*fileState) bool {
		candidates[id] = &fileState{mode: Inline, size: 0}
		return true
	})
	if err != nil {
		return 0, err
	}
	m.next++
	return id, nil
}

// Resize 修改文件大小（0 到 2^40）。
// 内联模式：size 不大于 A 且按该 size 放置合法则保持内联，否则转块模式；
// 块模式：size 为 0 回到内联模式，否则保持块模式（即使缩小到不大于 A）。
func (m *Manager) Resize(f int, size int64) error {
	if size < 0 || size > maxSize {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[f]; !ok {
		return ErrNotFound
	}
	return m.commitLocked(func(candidates map[int]*fileState) bool {
		file := candidates[f]
		newMode := file.mode
		if file.mode == Inline {
			if size <= m.A {
				if _, ok := m.place(Inline, size, file.xattrs); ok {
					newMode = Inline
				} else {
					newMode = Block
				}
			} else {
				newMode = Block
			}
		} else {
			if size == 0 {
				newMode = Inline
			}
		}
		// 新模式必须合法；块模式放不下时整体拒绝。
		if _, ok := m.place(newMode, size, file.xattrs); !ok {
			return false
		}
		file.mode = newMode
		file.size = size
		return true
	})
}

func validXattrArgs(name, value []byte) bool {
	return len(name) >= 1 && len(name) <= maxNameLen && len(value) <= maxValueLen
}

// SetXattr 设置（或覆盖同名）属性。
// 内联模式放置不合法则转块模式重算；块模式仍不合法则整体拒绝。
func (m *Manager) SetXattr(f int, name, value []byte) error {
	if !validXattrArgs(name, value) {
		return ErrInvalidArgument
	}
	name = append([]byte(nil), name...)
	value = append([]byte(nil), value...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[f]; !ok {
		return ErrNotFound
	}
	return m.commitLocked(func(candidates map[int]*fileState) bool {
		file := candidates[f]
		replaced := false
		for i := range file.xattrs {
			if bytes.Equal(file.xattrs[i].Name, name) {
				file.xattrs[i].Value = value
				replaced = true
				break
			}
		}
		if !replaced {
			file.xattrs = append(file.xattrs, Xattr{Name: name, Value: value})
			sortedXattrs(file.xattrs)
		}
		if _, ok := m.place(file.mode, file.size, file.xattrs); ok {
			return true
		}
		if file.mode == Inline {
			file.mode = Block
		}
		if _, ok := m.place(file.mode, file.size, file.xattrs); ok {
			return true
		}
		return false
	})
}

// RemoveXattr 删除属性。属性不存在返回 ErrNoXattr。
// 模式不会因删除而改变（块模式不回内联，由 Resize(…, 0) 触发）。
func (m *Manager) RemoveXattr(f int, name []byte) error {
	if len(name) < 1 || len(name) > maxNameLen {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[f]; !ok {
		return ErrNotFound
	}
	idx := -1
	for i := range m.files[f].xattrs {
		if bytes.Equal(m.files[f].xattrs[i].Name, name) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNoXattr
	}
	return m.commitLocked(func(candidates map[int]*fileState) bool {
		file := candidates[f]
		rm := -1
		for i := range file.xattrs {
			if bytes.Equal(file.xattrs[i].Name, name) {
				rm = i
				break
			}
		}
		file.xattrs = append(file.xattrs[:rm], file.xattrs[rm+1:]...)
		if _, ok := m.place(file.mode, file.size, file.xattrs); !ok {
			return false
		}
		return true
	})
}

// GetXattr 返回属性值的副本；属性不存在返回 ErrNoXattr。
func (m *Manager) GetXattr(f int, name []byte) ([]byte, error) {
	if len(name) < 1 || len(name) > maxNameLen {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	file, ok := m.files[f]
	if !ok {
		return nil, ErrNotFound
	}
	for _, x := range file.xattrs {
		if bytes.Equal(x.Name, name) {
			return append([]byte(nil), x.Value...), nil
		}
	}
	return nil, ErrNoXattr
}

// Clone 复制文件：复制模式、大小与全部属性；数据块独立占用，
// Ext 相同则与原文件（及其他文件）共享同一个外部块。
func (m *Manager) Clone(f int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[f]; !ok {
		return 0, ErrNotFound
	}
	id := m.next
	err := m.commitLocked(func(candidates map[int]*fileState) bool {
		candidates[id] = cloneFile(candidates[f])
		return true
	})
	if err != nil {
		return 0, err
	}
	m.next++
	return id, nil
}

// Stat 返回文件的模式、大小、数据块占用、Ext 编号与各属性落点。
// Ext 编号取与其 Ext 逐字节相同的全部文件中最小的文件编号；Ext 为空为 0。
func (m *Manager) Stat(f int) (*StatInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	file, ok := m.files[f]
	if !ok {
		return nil, ErrNotFound
	}
	p, legal := m.place(file.mode, file.size, file.xattrs)
	if !legal {
		panic(ErrXattrTooLarge)
	}
	info := &StatInfo{
		Mode:     file.mode,
		Size:     file.size,
		DataUsed: p.dataUsed,
		ExtID:    0,
		Xattrs:   make([]XattrLoc, len(file.xattrs)),
	}
	if p.external {
		extID := 0
		for id, other := range m.files {
			op, ok := m.place(other.mode, other.size, other.xattrs)
			if !ok {
				panic(ErrXattrTooLarge)
			}
			if op.external && bytes.Equal(op.extKey, p.extKey) {
				if extID == 0 || id < extID {
					extID = id
				}
			}
		}
		info.ExtID = extID
	}
	for i, x := range file.xattrs {
		info.Xattrs[i] = XattrLoc{Name: append([]byte(nil), x.Name...), Location: p.locations[i]}
	}
	return info, nil
}

// PoolUsed 返回当前块池占用（按定义从头重算），主要用于测试与观测。
func (m *Manager) PoolUsed() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.poolUsedLocked()
}
