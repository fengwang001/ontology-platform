package genericinst

// StaleInfo 描述一个实例的过期状态与原因。
type StaleInfo struct {
	ReasonDef string // 哪个定义的更新导致
	Depth     int    // 通过几层依赖传导而来（由被更新定义直接生成的实例为 0）
}

// Instance 是一次泛型实例化的登记记录。
type Instance struct {
	ID     int
	Def    string
	Args   []Type
	deps   map[int]struct{} // 它直接依赖的实例（parent -> child）
	depend map[int]struct{} // 直接依赖它的实例（child -> parent）
	stale  *StaleInfo
}

// Stale 返回过期信息；未过期时返回 nil。
func (in *Instance) Stale() *StaleInfo { return in.stale }

// Deps 返回直接依赖实例 ID 的有序快照。
func (in *Instance) Deps() []int { return sortedKeys(in.deps) }

// Dependents 返回直接依赖它的实例 ID 的有序快照。
func (in *Instance) Dependents() []int { return sortedKeys(in.depend) }

// invalidateLocked 处理一次定义更新：
//  1. 该定义当前仍有效的实例全部成为根（深度 0）；
//  2. 沿 depend 边反向 BFS，传递依赖它们的实例依次标记过期（深度递增）；
//  3. 被标记的实例移出命中索引。
//
// BFS 只访问「本次受影响集合」，不扫描无关实例。
func (r *Registry) invalidateLocked(reasonDef string) {
	visited := map[int]bool{}
	queue := make([]int, 0)
	for _, in := range r.instances {
		if in.Def == reasonDef && in.stale == nil {
			in.stale = &StaleInfo{ReasonDef: reasonDef, Depth: 0}
			visited[in.ID] = true
			queue = append(queue, in.ID)
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := r.instances[queue[head]]
		for pid := range cur.depend {
			parent := r.instances[pid]
			if parent == nil || parent.stale != nil || visited[parent.ID] {
				continue
			}
			parent.stale = &StaleInfo{ReasonDef: reasonDef, Depth: cur.stale.Depth + 1}
			visited[parent.ID] = true
			queue = append(queue, parent.ID)
		}
	}
	for id := range visited {
		in := r.instances[id]
		key := in.Def + "\x00" + argsKey(in.Args)
		delete(r.index, key)
		r.log.Log("stale", "id="+itoa(id), "def="+in.Def,
			"reason="+reasonDef, "depth="+itoa(in.stale.Depth))
	}
	r.staleVisits = len(visited)
	if len(visited) == 0 {
		r.log.Log("stale", "reason="+reasonDef, "affected=0")
	}
}

// Cleanup 清理一个实例：它必须已过期，且不再被任何未过期实例依赖。
// 不满足条件返回 ErrDependency；实例不存在也按依赖错误拒绝。
func (r *Registry) Cleanup(id int) *RequestError {
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.instances[id]
	if !ok {
		return &RequestError{Code: ErrDependency, Msg: "instance " + itoa(id) + " does not exist"}
	}
	if in.stale == nil {
		return &RequestError{Code: ErrDependency, Msg: "instance " + itoa(id) + " is not stale"}
	}
	for pid := range in.depend {
		if parent, ok := r.instances[pid]; ok && parent.stale == nil {
			return &RequestError{Code: ErrDependency,
				Msg: "stale instance " + itoa(id) + " is still depended on by live instance " + itoa(pid)}
		}
	}
	// 解除残留边后删除。
	for dep := range in.deps {
		if child, ok := r.instances[dep]; ok {
			delete(child.depend, id)
		}
	}
	for pid := range in.depend {
		if parent, ok := r.instances[pid]; ok {
			delete(parent.deps, id)
		}
	}
	delete(r.instances, id)
	r.log.Log("cleanup", "id="+itoa(id), "def="+in.Def, "result=deleted")
	return nil
}
