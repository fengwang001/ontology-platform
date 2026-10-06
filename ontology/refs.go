package ontology

// refEntry 是引用集合中一个全名的取值：要么直接指向对象，要么是指向另一
// 引用名的符号引用。
type refEntry struct {
	sym    bool
	target string // 直接引用：对象标识；符号引用：另一引用全名
}

// refHit 是短名解析的结果。
type refHit struct {
	terminal string // 符号引用链末端的引用全名（引用日志挂在其上）
	id       string // 末端直接引用指向的对象标识
	exact    bool   // 短名是否恰好就是某个全名
	dangling bool   // 链末端是否悬空（指向不存在的引用）
	probes   int    // 本次查找的 map 探针数（性能证明用）
}

// refStore 是引用集合：全名到对象的映射 + 符号引用 + 每个直接引用的日志。
// 短名补全按固定命名空间次序逐个 map 查找，代价与引用总数无关。
type refStore struct {
	entries    map[string]refEntry
	logs       map[string][]string // 直接引用全名 -> 历史对象标识（旧->新）
	namespaces []string            // 补全命名空间，有序
}

func newRefStore(namespaces []string) *refStore {
	return &refStore{
		entries:    make(map[string]refEntry),
		logs:       make(map[string][]string),
		namespaces: namespaces,
	}
}

// set 把全名设为指向 id 的直接引用，并追加一条引用日志。
func (r *refStore) set(name, id string) {
	r.entries[name] = refEntry{target: id}
	r.logs[name] = append(r.logs[name], id)
}

// setSymbolic 把 name 设为指向 target 的符号引用；若会成环则整体拒绝
// （不改变任何状态），返回 false。
func (r *refStore) setSymbolic(name, target string) bool {
	cur := target
	for i := 0; i <= len(r.entries); i++ { // 链长受条目数所限，必终止
		if cur == name {
			return false
		}
		e, ok := r.entries[cur]
		if !ok || !e.sym {
			break
		}
		cur = e.target
	}
	r.entries[name] = refEntry{sym: true, target: target}
	return true
}

// lookup 解析短名：先按全名精确命中，否则按命名空间次序补全，首个命中者
// 胜出；命中后沿符号引用链走到末端直接引用。
func (r *refStore) lookup(short string) (refHit, bool) {
	probes := 0
	try := func(name string, exact bool) (refHit, bool) {
		probes++
		e, ok := r.entries[name]
		if !ok {
			return refHit{}, false
		}
		cur := name
		for e.sym {
			probes++
			cur = e.target
			e, ok = r.entries[cur]
			if !ok {
				return refHit{terminal: cur, exact: exact, dangling: true, probes: probes}, true
			}
		}
		return refHit{terminal: cur, id: e.target, exact: exact, probes: probes}, true
	}
	if h, ok := try(short, true); ok {
		return h, true
	}
	for _, ns := range r.namespaces {
		if h, ok := try(ns+short, false); ok {
			return h, true
		}
	}
	return refHit{probes: probes}, false
}

// reflog 返回直接引用全名的日志（旧->新）。
func (r *refStore) reflog(name string) []string {
	return r.logs[name]
}
