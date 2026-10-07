package bitemporal

// Snapshot 在持有存储读锁期间保证写入无法插入，因此查询永远不会观察到
// 「新区间已写入而写入时间记录尚未落定」之类的中间状态：
// 一条记录对查询要么整体可见，要么完全不可见。
//
// 注意：快照方法不得重复获取读锁。Go 的 sync.RWMutex 不保证同 goroutine
// 重入 RLock——当有写者等待时，第二次 RLock 会排在写者之后，形成自死锁。
// 因此一次 Snapshot/Release 恰好对应一次 RLock/RUnlock。

// Snapshot 是 Store 在某一时刻的一致性只读视图。
type Snapshot struct {
	objects map[string][]ObjectRecord
	links   map[string][]LinkRecord
	out     map[string][]string
	release func()
}

// ObjectVersions 返回某对象的全部写入记录（按写入时间升序）。
func (snap *Snapshot) ObjectVersions(id string) []ObjectRecord {
	versions := snap.objects[id]
	out := make([]ObjectRecord, len(versions))
	copy(out, versions)
	return out
}

// LinkVersions 返回某链接的全部写入记录（按写入时间升序）。
func (snap *Snapshot) LinkVersions(id string) []LinkRecord {
	versions := snap.links[id]
	out := make([]LinkRecord, len(versions))
	copy(out, versions)
	return out
}

// OutLinks 返回以 id 为源端点的全部链接的全部版本。
func (snap *Snapshot) OutLinks(id string) []LinkRecord {
	linkIDs := snap.out[id]
	all := make([]LinkRecord, 0)
	for _, linkID := range linkIDs {
		all = append(all, snap.links[linkID]...)
	}
	return all
}

// Release 释放快照持有的读锁。
func (snap *Snapshot) Release() {
	snap.release()
}
