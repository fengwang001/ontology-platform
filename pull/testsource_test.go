package pull_test

import (
	"errors"
	"sync"

	"ontology/pull"
)

// srcRow 是测试源端存储的行：(id,ts,ver,sv,commitAt)。
type srcRow struct {
	id, ts, ver, sv, commit int64
}

// memSource 是按规格朴素实现的源端：
// 每个 id 先筛出 ts<=maxTs 且 commitAt<=visibleAt 的版本，只保留 ver 最大者；
// 再按 (ts,id) > (afterTs,afterID) 过滤，按 (ts,id) 升序取前 limit 行。
// failAtQueries>0 时在第 failAtQueries 次 Query（本轮计数，从 1 起）返回错误。
type memSource struct {
	mu            sync.Mutex
	rows          []srcRow
	calls         int
	failAtQueries int
}

func (m *memSource) reset() {
	m.mu.Lock()
	m.calls = 0
	m.mu.Unlock()
}

func (m *memSource) Query(afterTs, afterID, limit, maxTs, visibleAt int64) ([]pull.Row, error) {
	m.mu.Lock()
	m.calls++
	attempt := m.calls
	failAt := m.failAtQueries
	m.mu.Unlock()
	if failAt > 0 && attempt == failAt {
		return nil, errors.New("boom")
	}
	latest := make(map[int64]srcRow)
	for _, r := range m.rows {
		if r.ts > maxTs || r.commit > visibleAt {
			continue
		}
		if cur, ok := latest[r.id]; !ok || r.ver > cur.ver {
			latest[r.id] = r
		}
	}
	var out []pull.Row
	for _, r := range latest {
		if r.ts < afterTs || (r.ts == afterTs && r.id <= afterID) {
			continue
		}
		out = append(out, pull.Row{ID: r.id, Ts: r.ts, Ver: r.ver, Sv: r.sv})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j].Ts < out[j-1].Ts ||
				(out[j].Ts == out[j-1].Ts && out[j].ID < out[j-1].ID) {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}
