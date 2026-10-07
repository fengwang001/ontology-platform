package ontology

// Store 是对象实例与链接关系的存储抽象。所有方法在 Engine 持锁期间被调用。
type Store interface {
	Get(id InstanceID) (Instance, bool)
	Has(id InstanceID) bool
	// Neighbors 返回经由指定规则、从 id 出发一步可达的实例及链接类型。
	Neighbors(id InstanceID, rule CascadeRule) []InstanceID
	Snapshot() WorldState
	Clock() int64
	Audit() []AuditRecord
	AppendAudit(rec AuditRecord)

	// 以下方法作用于内部暂存区，Commit 后才对外可见。
	Begin()
	stageCreate(op DirectOp)
	stageUpdate(id InstanceID, attrs map[string]string)
	stageDelete(id InstanceID)
	stageLink(e Edge)
	stageUnlink(e Edge)
	commit()
	rollback()
}
