package ontology

import (
	"sort"
	"strconv"
	"sync"
)

// Manager 管理带版本与引用生命周期的配置快照。
type Manager struct {
	mu sync.Mutex

	decls map[string]FieldDecl

	versions map[int64]*versionData
	current  int64
	counter  int64

	refs   map[int64]int
	active map[*Snapshot]int64
	nextID int64
}

type versionData struct {
	values  Fields
	sources map[string]int64
}

// NewManager 创建管理器并写入第 1 版（版本号从 1 开始）。
// 初始内容必须为每个已声明字段提供合法初值，字段名重复的声明以最后一个为准。
func NewManager(decls []FieldDecl, initial Fields) (*Manager, error) {
	m := &Manager{
		decls:    make(map[string]FieldDecl, len(decls)),
		versions: make(map[int64]*versionData),
		refs:     make(map[int64]int),
		active:   make(map[*Snapshot]int64),
	}
	for _, d := range decls {
		m.decls[d.Name] = d
	}
	if errs := m.validate(initial); len(errs) > 0 {
		return nil, errs
	}
	for name := range m.decls {
		if _, ok := initial[name]; !ok {
			return nil, ValidationErrors{{
				Field:  name,
				Kind:   FailureTypeMismatch,
				Detail: "missing initial value for declared field",
			}}
		}
	}
	values := make(Fields, len(initial))
	sources := make(map[string]int64, len(initial))
	for name, v := range initial {
		values[name] = v
		sources[name] = 1
	}
	m.versions[1] = &versionData{values: values, sources: sources}
	m.current = 1
	m.counter = 1
	return m, nil
}

// CurrentVersion 返回当前版本号。
func (m *Manager) CurrentVersion() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Acquire 取一份指向当前版本的快照，并把该版本的引用计数加一。
func (m *Manager) Acquire() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	snap := &Snapshot{id: m.nextID, version: m.current, mgr: m}
	m.active[snap] = m.current
	m.refs[m.current]++
	return snap
}

// release 是 Snapshot.Release 的内部实现，返回是否为首次归还。
func (m *Manager) release(s *Snapshot) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.active[s]
	if !ok {
		return false
	}
	delete(m.active, s)
	m.refs[v]--
	if m.refs[v] == 0 {
		delete(m.refs, v)
	}
	return true
}

// Reclaim 回收没有任何未归还快照指向、且不是当前版本的历史版本。
// 版本数据删除后，指向它的过期快照再读取会得到 ErrVersionReclaimed。
func (m *Manager) Reclaim() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var reclaimed []int64
	for v := range m.versions {
		if v == m.current || m.refs[v] > 0 {
			continue
		}
		delete(m.versions, v)
		reclaimed = append(reclaimed, v)
	}
	sort.Slice(reclaimed, func(i, j int) bool { return reclaimed[i] < reclaimed[j] })
	return reclaimed
}

// Restore 用某个历史版本的内容生成一个更大的新版本，版本号不会回到过去。
func (m *Manager) Restore(version int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.versions[version]
	if !ok {
		return 0, &ValidationError{
			Field:  "",
			Kind:   FailureUnknownField,
			Detail: "version not found: " + strconv.FormatInt(version, 10),
		}
	}
	nextVersion := m.counter + 1
	values := make(Fields, len(src.values))
	sources := make(map[string]int64, len(src.sources))
	for name, v := range src.values {
		values[name] = v
	}
	for name, sver := range src.sources {
		sources[name] = sver
	}
	m.counter = nextVersion
	m.versions[nextVersion] = &versionData{values: values, sources: sources}
	m.current = nextVersion
	return nextVersion, nil
}

// Content 返回某个版本（含历史版本）的字段内容副本。
func (m *Manager) Content(version int64) (Fields, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vd, ok := m.versions[version]
	if !ok {
		return nil, false
	}
	out := make(Fields, len(vd.values))
	for name, v := range vd.values {
		out[name] = v
	}
	return out, true
}

// LeakReport 描述当前未归还的快照。
type LeakReport struct {
	Outstanding int
	Snapshots   []SnapRef
}

// SnapRef 指出一份未归还快照的编号与其指向的版本。
type SnapRef struct {
	SnapshotID int64
	Version    int64
}

// Leaks 返回当前未归还快照的泄漏报告（按快照编号排序）。
func (m *Manager) Leaks() LeakReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	report := LeakReport{Outstanding: len(m.active)}
	for s, v := range m.active {
		report.Snapshots = append(report.Snapshots, SnapRef{SnapshotID: s.id, Version: v})
	}
	sort.Slice(report.Snapshots, func(i, j int) bool {
		return report.Snapshots[i].SnapshotID < report.Snapshots[j].SnapshotID
	})
	return report
}
