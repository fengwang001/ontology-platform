package ontology

// Occupancy 是一次独占占用权的句柄，持有期间变更先暂存，Commit 时原子生效。
type Occupancy struct {
	store    *Store
	instance InstanceID
	holder   ActionID
	token    uint64
	staged   []Mutation
	done     bool
}

// Instance 返回被占用的实例。
func (o *Occupancy) Instance() InstanceID { return o.instance }

// Token 返回本次占用的栅栏令牌。
func (o *Occupancy) Token() uint64 { return o.token }

// Apply 暂存一组属性变更（含生命周期状态机转换），提交前对外不可见。
func (o *Occupancy) Apply(muts ...Mutation) error {
	return o.store.stage(o, muts)
}

// Heartbeat 续期租约；租约已失效时返回 RejectOccupancyLost。
func (o *Occupancy) Heartbeat() error {
	return o.store.heartbeat(o)
}

// Commit 原子地应用全部暂存变更、递增版本并释放占用。
func (o *Occupancy) Commit() (uint64, error) {
	return o.store.commit(o)
}

// Abort 丢弃暂存变更并释放占用。
func (o *Occupancy) Abort() error {
	return o.store.abort(o)
}
