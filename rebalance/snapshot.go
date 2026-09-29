package rebalance

import (
	"encoding/json"
	"fmt"
	"sort"
)

// snapData 是崩溃恢复用的序列化格式，包含全部记录与迁移态。
type snapData struct {
	N          int                       `json:"n"`
	Migrating  bool                      `json:"migrating"`
	FromN      int                       `json:"from_n,omitempty"`
	ToN        int                       `json:"to_n,omitempty"`
	Cursor     int                       `json:"cursor,omitempty"`
	Plan       []string                  `json:"plan,omitempty"`
	Partitions map[int]map[string]string `json:"partitions"`
}

// Snapshot 导出当前全部状态（含迁移态与游标），可落盘用于崩溃恢复。
// 该快照在锁内一次性生成，与任意时刻的内存状态一致、可复现。
func (m *Migrator) Snapshot() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limit := m.n
	if m.migrating {
		limit = maxInt(m.fromN, m.toN)
	}
	parts := make(map[int]map[string]string, limit)
	for p := 0; p < limit; p++ {
		cp := make(map[string]string, len(m.parts[p]))
		for k, v := range m.parts[p] {
			cp[k] = v
		}
		parts[p] = cp
	}
	snap := snapData{
		N:          m.n,
		Migrating:  m.migrating,
		FromN:      m.fromN,
		ToN:        m.toN,
		Cursor:     m.cursor,
		Plan:       append([]string(nil), m.plan...),
		Partitions: parts,
	}
	return json.Marshal(snap)
}

// Restore 从快照恢复，并校验全部迁移不变量：
//   - 清单必须等于“归属改变”的键集合且按键排序；
//   - plan[:cursor] 的键必须恰好位于新归属分区；
//   - plan[cursor:] 的键必须恰好位于旧归属分区；
//   - 非清单键必须位于旧归属分区；
//
// 校验失败则整体拒绝，绝不返回一个半恢复的迁移器。
// 恢复后继续 Step：已迁移的键不会重复迁移，未迁移的键不会跳过。
func Restore(data []byte) (*Migrator, error) {
	var snap snapData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	if snap.N < 1 {
		return nil, fmt.Errorf("restore: %w", ErrInvalidPartitionCount)
	}

	limit := snap.N
	if snap.Migrating {
		if snap.FromN != snap.N || snap.ToN < 1 {
			return nil, fmt.Errorf("restore: %w", ErrInvalidPartitionCount)
		}
		limit = maxInt(snap.FromN, snap.ToN)
	}
	parts := make([]map[string]string, limit)
	for p := 0; p < limit; p++ {
		parts[p] = make(map[string]string)
	}
	for p, kv := range snap.Partitions {
		if p < 0 || p >= limit {
			return nil, fmt.Errorf("restore: partition %d out of range", p)
		}
		for k, v := range kv {
			parts[p][k] = v
		}
	}

	m := &Migrator{n: snap.N, parts: parts}
	if !snap.Migrating {
		if err := m.verifyStable(); err != nil {
			return nil, err
		}
		return m, nil
	}

	plan := append([]string(nil), snap.Plan...)
	expected := make([]string, 0)
	allKeys := make(map[string]int)
	for p := 0; p < limit; p++ {
		for k := range parts[p] {
			if prev, dup := allKeys[k]; dup {
				return nil, fmt.Errorf("restore: key %q duplicated in p%d and p%d", k, prev, p)
			}
			allKeys[k] = p
			if ownerOf(k, snap.FromN) != ownerOf(k, snap.ToN) {
				expected = append(expected, k)
			}
		}
	}
	sort.Strings(expected)
	if len(plan) != len(expected) || snap.Cursor < 0 || snap.Cursor > len(plan) {
		return nil, fmt.Errorf("restore: %w", ErrMigrationIncomplete)
	}
	for i := range expected {
		if plan[i] != expected[i] {
			return nil, fmt.Errorf("restore: plan mismatch at %d: %q vs %q", i, plan[i], expected[i])
		}
	}

	inPlan := make(map[string]bool, len(plan))
	for i, k := range plan {
		if inPlan[k] {
			return nil, fmt.Errorf("restore: duplicate plan key %q", k)
		}
		inPlan[k] = true
		want := ownerOf(k, snap.FromN)
		if i < snap.Cursor {
			want = ownerOf(k, snap.ToN)
		}
		if got, ok := allKeys[k]; !ok || got != want {
			return nil, fmt.Errorf("restore: key %q not at expected partition %d", k, want)
		}
	}
	for k, got := range allKeys {
		if inPlan[k] {
			continue
		}
		oldP := ownerOf(k, snap.FromN)
		if ownerOf(k, snap.ToN) != oldP || got != oldP {
			return nil, fmt.Errorf("restore: non-plan key %q outside old owner p%d", k, oldP)
		}
	}

	index := make(map[string]int, len(plan))
	for i, k := range plan {
		index[k] = i
	}
	m.migrating = true
	m.fromN = snap.FromN
	m.toN = snap.ToN
	m.plan = plan
	m.planIndex = index
	m.cursor = snap.Cursor
	return m, nil
}

// verifyStable 校验非迁移态：每条记录都必须在 hash(key) mod n 分区。
func (m *Migrator) verifyStable() error {
	for p := 0; p < m.n; p++ {
		for k := range m.parts[p] {
			if ownerOf(k, m.n) != p {
				return fmt.Errorf("restore: key %q not at owner partition %d", k, p)
			}
		}
	}
	return nil
}
