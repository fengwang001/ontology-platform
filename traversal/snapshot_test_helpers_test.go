package traversal

import "reflect"

// enableSnapshotHistory 开启快照留存（测试专用），并把当前快照作为版本 v0。
func enableSnapshotHistory(g *Graph) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.snapshotHistory = []*snapshot{g.cur.Load()}
}

func snapshotAtVersion(g *Graph, version uint64) *snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.snapshotHistory {
		if s.version == version {
			return s
		}
	}
	// 历史未开启时直接返回当前快照。
	return g.cur.Load()
}

func reflectResultsEqual(a, b *TraversalResult) bool {
	if a.SnapshotVersion != b.SnapshotVersion {
		return false
	}
	return reflect.DeepEqual(shapes(a), shapes(b))
}
