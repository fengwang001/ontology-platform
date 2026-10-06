package backupretention

import (
	"sort"
	"time"
)

// naiveBackup 是独立朴素模型的一条备份记录。
// 该模型刻意用“最直白、可能低效”的方式直接按题面文字实现，
// 不共享生产代码中的任何数据结构与算法（仅复用 Layer/Policy 类型做输入）。
type naiveBackup struct {
	id        string
	parent    string // "" 表示全量
	size      int64
	createdAt int64
	corrupt   bool
	legal     bool
}

type naiveModel struct {
	backups   map[string]*naiveBackup
	policy    Policy
	lastClock int64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{backups: map[string]*naiveBackup{}, lastClock: -1}
}

type naiveOpResult struct {
	errKind ErrorKind // KindUnknown 表示成功
	plan    *Plan
	deleted []string
}

func validBasic(id string, size, ts int64) bool {
	return id != "" && size >= 0 && size <= MaxBackupSize &&
		ts >= 0 && ts <= MaxTimestamp
}

func (m *naiveModel) registerFull(id string, size, ts int64) ErrorKind {
	if !validBasic(id, size, ts) {
		return KindInvalidParam
	}
	if ts < m.lastClock {
		return KindClockSkew
	}
	if _, exists := m.backups[id]; exists {
		return KindDuplicateID
	}
	m.backups[id] = &naiveBackup{id: id, size: size, createdAt: ts}
	m.lastClock = ts
	return KindUnknown
}

func (m *naiveModel) registerIncr(id string, size, ts int64, parent string) ErrorKind {
	if !validBasic(id, size, ts) || parent == "" {
		return KindInvalidParam
	}
	if ts < m.lastClock {
		return KindClockSkew
	}
	if _, exists := m.backups[id]; exists {
		return KindDuplicateID
	}
	p, ok := m.backups[parent]
	if !ok {
		return KindParentNotFound
	}
	if ts < p.createdAt {
		return KindTimeContradiction
	}
	m.backups[id] = &naiveBackup{id: id, parent: parent, size: size, createdAt: ts}
	m.lastClock = ts
	return KindUnknown
}

func (m *naiveModel) markCorrupt(id string, now int64) ErrorKind {
	if id == "" || now < 0 || now > MaxTimestamp {
		return KindInvalidParam
	}
	if now < m.lastClock {
		return KindClockSkew
	}
	b, ok := m.backups[id]
	if !ok {
		return KindBackupNotFound
	}
	b.corrupt = true
	m.lastClock = now
	return KindUnknown
}

func (m *naiveModel) setLegal(id string, now int64, on bool) ErrorKind {
	if id == "" || now < 0 || now > MaxTimestamp {
		return KindInvalidParam
	}
	if now < m.lastClock {
		return KindClockSkew
	}
	b, ok := m.backups[id]
	if !ok {
		return KindBackupNotFound
	}
	b.legal = on
	m.lastClock = now
	return KindUnknown
}

func (m *naiveModel) setPolicy(p Policy, now int64) ErrorKind {
	if now < 0 || now > MaxTimestamp {
		return KindInvalidParam
	}
	if now < m.lastClock {
		return KindClockSkew
	}
	if p.Daily < 0 || p.Daily > 1000 || p.Weekly < 0 || p.Weekly > 1000 ||
		p.Monthly < 0 || p.Monthly > 1000 {
		return KindLayerOutOfRange
	}
	m.policy = p
	m.lastClock = now
	return KindUnknown
}

// naivePeriod 用标准库 time 直接计算周期，绝不复用生产代码的 periodOf。
func naivePeriod(layer Layer, ts int64) int64 {
	u := time.Unix(ts, 0).UTC()
	y, mo, d := u.Date()
	switch layer {
	case LayerDaily:
		return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
	case LayerMonthly:
		return int64(y)*12 + int64(mo) - 1
	default: // 周
		monday := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
		for monday.Weekday() != time.Monday {
			monday = monday.AddDate(0, 0, -1)
		}
		return monday.Unix() / 86400
	}
}

func (m *naiveModel) recoverable(b *naiveBackup) bool {
	for b != nil {
		if b.corrupt {
			return false
		}
		if b.parent == "" {
			return true
		}
		b = m.backups[b.parent]
	}
	return true
}

// naivePlan 严格按题面逐层、逐周期、逐备份扫描（故意 O(n^2) 风格，保持直观）。
func (m *naiveModel) plan(now int64) (*Plan, ErrorKind) {
	if now < 0 || now > MaxTimestamp {
		return nil, KindInvalidParam
	}
	if now < m.lastClock {
		return nil, KindClockSkew
	}

	type seat struct {
		b     *naiveBackup
		layer Layer
	}
	var seats []seat

	layers := []struct {
		layer Layer
		n     int
	}{
		{LayerDaily, m.policy.Daily},
		{LayerWeekly, m.policy.Weekly},
		{LayerMonthly, m.policy.Monthly},
	}
	for _, L := range layers {
		if L.n == 0 {
			continue
		}
		c := naivePeriod(L.layer, now)
		// 对最近 N 个周期逐个枚举；每个周期独立扫描所有备份挑代表。
		for off := int64(0); off < int64(L.n); off++ {
			p := c - off
			var winner *naiveBackup
			for _, b := range m.backups {
				if b.createdAt > now || !m.recoverable(b) {
					continue
				}
				if naivePeriod(L.layer, b.createdAt) != p {
					continue
				}
				if winner == nil ||
					b.createdAt > winner.createdAt ||
					(b.createdAt == winner.createdAt && b.id > winner.id) {
					winner = b
				}
			}
			if winner != nil {
				seats = append(seats, seat{winner, L.layer})
			}
		}
	}

	direct := map[string]map[Layer]bool{}
	ensure := func(id string) map[Layer]bool {
		s, ok := direct[id]
		if !ok {
			s = map[Layer]bool{}
			direct[id] = s
		}
		return s
	}
	for _, st := range seats {
		ensure(st.b.id)[st.layer] = true
	}
	legal := map[string]bool{}
	for id, b := range m.backups {
		if b.legal {
			legal[id] = true
		}
	}

	// 保留根集合 = 直接保留 ∪ 法律保留；沿父链闭包标记依赖保护。
	dependency := map[string]bool{}
	roots := map[string]bool{}
	for id := range direct {
		roots[id] = true
	}
	for id := range legal {
		roots[id] = true
	}
	for id := range roots {
		cur := m.backups[id]
		for cur.parent != "" {
			cur = m.backups[cur.parent]
			dependency[cur.id] = true
		}
	}

	out := &Plan{Now: now, Policy: m.policy}
	for id, b := range m.backups {
		pb := PlannedBackup{ID: id, CreatedAt: b.createdAt}
		if direct[id] != nil || legal[id] || dependency[id] {
			pb.Kept = true
			pb.Reason = RetentionReason{
				Dependency: dependency[id],
				LegalHold:  legal[id],
			}
			if direct[id][LayerDaily] {
				pb.Reason.DirectLayers = append(pb.Reason.DirectLayers, LayerDaily)
			}
			if direct[id][LayerWeekly] {
				pb.Reason.DirectLayers = append(pb.Reason.DirectLayers, LayerWeekly)
			}
			if direct[id][LayerMonthly] {
				pb.Reason.DirectLayers = append(pb.Reason.DirectLayers, LayerMonthly)
			}
			out.Retained = append(out.Retained, pb)
		} else {
			pb.Reason = RetentionReason{DirectLayers: []Layer{}}
			out.Deletable = append(out.Deletable, pb)
		}
	}
	sortPlanned(out.Retained)
	sortPlanned(out.Deletable)
	m.lastClock = now // 只读计划同样推进时钟基线
	return out, KindUnknown
}

func (m *naiveModel) cleanup(now int64) (*Plan, []string, ErrorKind) {
	p, k := m.plan(now)
	if k != KindUnknown {
		return nil, nil, k
	}
	var deleted []string
	for _, b := range p.Deletable {
		deleted = append(deleted, b.ID)
		delete(m.backups, b.ID)
	}
	sort.Strings(deleted)
	return p, deleted, KindUnknown
}
