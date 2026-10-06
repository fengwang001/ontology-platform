package idb

// database 持有名字、当前版本、仓库集合与一个独立的作用域调度器。
// 所有字段仅由 kernel 的单个 goroutine 访问，不需要自身加锁。
type database struct {
	name        string
	version     int
	stores      map[string]*ObjectStore
	sched       *scheduler
	connections map[*Connection]struct{}
	k           *Kernel

	// upgradeConn 非 nil 表示版本变更事务正在运行（该连接独占数据库）。
	upgradeConn *Connection

	// 阻塞中的版本变更 / 删除请求（至多一个，后到者排队其后）。
	blocker *blocker
}

func newDatabase(name string) *database {
	return &database{
		name:        name,
		version:     0,
		stores:      make(map[string]*ObjectStore),
		sched:       newScheduler(),
		connections: make(map[*Connection]struct{}),
	}
}

func (d *database) hasStore(name string) bool {
	_, ok := d.stores[name]
	return ok
}

func (d *database) createStore(name string) *ObjectStore {
	s := newObjectStore(name)
	d.stores[name] = s
	return s
}

func (d *database) deleteStore(name string) { delete(d.stores, name) }

func (d *database) storeNames() []string {
	names := make([]string, 0, len(d.stores))
	for n := range d.stores {
		names = append(names, n)
	}
	return names
}

func (d *database) snapshotStores() []string { return d.storeNames() }

// cloneStores 深拷贝全部仓库（版本变更中止恢复用）。
func (d *database) cloneStores() map[string]*ObjectStore {
	cp := make(map[string]*ObjectStore, len(d.stores))
	for n, s := range d.stores {
		cp[n] = s.clone()
	}
	return cp
}

// commitSeq 每次普通读写事务提交/版本变更提交单调递增，作为快照时间线。
type commitSeq = uint64
