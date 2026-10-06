// Package naive 是 scrub.Service 的独立朴素模型：
// 语义相同，但刻意使用最直接的实现（全量扫描、线性查找），
// 用于随机操作序列的差分对照测试。
package naive

import (
	"fmt"
	"sort"
	"sync"

	"ontology/scrub"
)

// Model 是朴素实现的仲裁服务。
type Model struct {
	mu     sync.Mutex
	blocks map[uint64]*block
	last   int64
	hasT   bool
	alerts []scrub.Alert
}

type block struct {
	minInterval int64
	replicas    map[uint64]scrub.Replica
	scrubbed    bool
	lastScrub   int64
}

// New 创建朴素模型。
func New() *Model {
	return &Model{blocks: make(map[uint64]*block)}
}

func (m *Model) checkClock(now int64) error {
	if m.hasT && now < m.last {
		return fmt.Errorf("%w: now %d < last accepted %d", scrub.ErrClockRegression, now, m.last)
	}
	return nil
}

func distinct(nodes []uint64) bool {
	for i := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i] == nodes[j] {
				return false
			}
		}
	}
	return true
}

func (m *Model) CreateBlock(id uint64, nodes []uint64, version uint64, digest string, minInterval int64, now int64) error {
	if now < 0 || version == 0 || digest == "" || minInterval < 0 ||
		len(nodes) < 2 || len(nodes) > 5 || !distinct(nodes) {
		return fmt.Errorf("%w: create block %d", scrub.ErrInvalidParam, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.blocks[id]; ok {
		return fmt.Errorf("%w: block %d already exists", scrub.ErrInvalidParam, id)
	}
	b := &block{minInterval: minInterval, replicas: make(map[uint64]scrub.Replica, len(nodes))}
	for _, n := range nodes {
		b.replicas[n] = scrub.Replica{NodeID: n, Version: version, Stored: digest, Actual: digest}
	}
	m.blocks[id] = b
	m.last, m.hasT = now, true
	return nil
}

func (m *Model) Write(id uint64, version uint64, digest string, nodes []uint64, now int64) error {
	if now < 0 || version == 0 || digest == "" || len(nodes) == 0 || !distinct(nodes) {
		return fmt.Errorf("%w: write block %d", scrub.ErrInvalidParam, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return err
	}
	b, ok := m.blocks[id]
	if !ok {
		return fmt.Errorf("%w: %d", scrub.ErrBlockNotFound, id)
	}
	for _, n := range nodes {
		if _, ok := b.replicas[n]; !ok {
			return fmt.Errorf("%w: node %d holds no replica of block %d", scrub.ErrInvalidParam, n, id)
		}
	}
	for _, n := range nodes {
		b.replicas[n] = scrub.Replica{NodeID: n, Version: version, Stored: digest, Actual: digest}
	}
	m.last, m.hasT = now, true
	return nil
}

func (m *Model) InjectBitrot(id, nodeID uint64, actualDigest string, now int64) error {
	if now < 0 || actualDigest == "" {
		return fmt.Errorf("%w: bitrot block %d", scrub.ErrInvalidParam, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return err
	}
	b, ok := m.blocks[id]
	if !ok {
		return fmt.Errorf("%w: %d", scrub.ErrBlockNotFound, id)
	}
	r, ok := b.replicas[nodeID]
	if !ok {
		return fmt.Errorf("%w: node %d holds no replica of block %d", scrub.ErrInvalidParam, nodeID, id)
	}
	r.Actual = actualDigest
	b.replicas[nodeID] = r
	m.last, m.hasT = now, true
	return nil
}

func (m *Model) DropReplica(id, nodeID uint64, now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: drop replica of block %d", scrub.ErrInvalidParam, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return err
	}
	b, ok := m.blocks[id]
	if !ok {
		return fmt.Errorf("%w: %d", scrub.ErrBlockNotFound, id)
	}
	if _, ok := b.replicas[nodeID]; !ok {
		return fmt.Errorf("%w: node %d holds no replica of block %d", scrub.ErrInvalidParam, nodeID, id)
	}
	delete(b.replicas, nodeID)
	if len(b.replicas) == 0 {
		delete(m.blocks, id)
	}
	m.last, m.hasT = now, true
	return nil
}

func (m *Model) Scrub(id uint64, now int64, failNodes map[uint64]bool) (scrub.ScrubResult, error) {
	if now < 0 {
		return scrub.ScrubResult{}, fmt.Errorf("%w: scrub block %d", scrub.ErrInvalidParam, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return scrub.ScrubResult{}, err
	}
	b, ok := m.blocks[id]
	if !ok {
		return scrub.ScrubResult{}, fmt.Errorf("%w: %d", scrub.ErrBlockNotFound, id)
	}
	if b.scrubbed && now-b.lastScrub < b.minInterval {
		return scrub.ScrubResult{}, fmt.Errorf("%w: block %d", scrub.ErrTooFrequent, id)
	}

	replicas := make([]scrub.Replica, 0, len(b.replicas))
	for _, r := range b.replicas {
		replicas = append(replicas, r)
	}
	v := scrub.Arbitrate(replicas)

	res := scrub.ScrubResult{
		Outcome:          v.Outcome,
		CommittedVersion: v.CommittedVersion,
		AuthVersion:      v.AuthVersion,
		AuthDigest:       v.AuthDigest,
	}
	switch {
	case v.Outcome.Unrepairable():
		m.alerts = append(m.alerts, scrub.Alert{
			Seq:                      len(m.alerts),
			BlockID:                  id,
			Time:                     now,
			Outcome:                  v.Outcome,
			CommittedVersion:         v.CommittedVersion,
			HasSelfConsistent:        v.Outcome != scrub.OutcomeNoSource,
			MaxSelfConsistentVersion: v.AuthVersion,
		})
	case len(v.RepairTargets) > 0:
		for _, n := range v.RepairTargets {
			if failNodes[n] {
				res.Failed = append(res.Failed, n)
				continue
			}
			b.replicas[n] = scrub.Replica{NodeID: n, Version: v.AuthVersion, Stored: v.AuthDigest, Actual: v.AuthDigest}
			res.Repaired = append(res.Repaired, n)
		}
		if len(res.Failed) > 0 {
			res.Outcome = scrub.OutcomePartialRepair
		}
	}

	b.scrubbed = true
	b.lastScrub = now
	m.last, m.hasT = now, true
	return res, nil
}

func (m *Model) SelectDue(now int64, limit int) ([]uint64, error) {
	if now < 0 || limit <= 0 {
		return nil, fmt.Errorf("%w: select due limit %d", scrub.ErrInvalidParam, limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	type cand struct {
		id        uint64
		scrubbed  bool
		lastScrub int64
	}
	var due []cand
	for id, b := range m.blocks {
		if !b.scrubbed || now-b.lastScrub >= b.minInterval {
			due = append(due, cand{id, b.scrubbed, b.lastScrub})
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].scrubbed != due[j].scrubbed {
			return !due[i].scrubbed
		}
		if due[i].scrubbed && due[i].lastScrub != due[j].lastScrub {
			return due[i].lastScrub < due[j].lastScrub
		}
		return due[i].id < due[j].id
	})
	if len(due) > limit {
		due = due[:limit]
	}
	ids := make([]uint64, 0, len(due))
	for _, c := range due {
		ids = append(ids, c.id)
	}
	return ids, nil
}

func (m *Model) Alerts() []scrub.Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]scrub.Alert, len(m.alerts))
	copy(out, m.alerts)
	return out
}

func (m *Model) Inspect(id uint64) (scrub.BlockInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[id]
	if !ok {
		return scrub.BlockInfo{}, fmt.Errorf("%w: %d", scrub.ErrBlockNotFound, id)
	}
	info := scrub.BlockInfo{
		ID:          id,
		MinInterval: b.minInterval,
		Scrubbed:    b.scrubbed,
		LastScrub:   b.lastScrub,
		Replicas:    make([]scrub.Replica, 0, len(b.replicas)),
	}
	for _, r := range b.replicas {
		info.Replicas = append(info.Replicas, r)
	}
	sort.Slice(info.Replicas, func(i, j int) bool { return info.Replicas[i].NodeID < info.Replicas[j].NodeID })
	return info, nil
}
