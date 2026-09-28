package vv

// DiffEntry 是同步差集中的一条变更。
type DiffEntry = Change

// ComputeDiff 由来源根据目标版本向量计算最小差集，按 (来源, 序号) 有序。
func (rep *Replica) ComputeDiff(target VersionVector) ([]DiffEntry, error) {
	return nil, nil
}

// ApplyDiff 由目标按序应用差集，校验通过后原子提交。
func (rep *Replica) ApplyDiff(diff []DiffEntry) error {
	return nil
}

// Sync 执行 source -> target 的单向增量同步，返回实际发送并应用的差集。
func Sync(source, target *Replica) ([]DiffEntry, error) {
	return nil, nil
}

// SyncBoth 执行两个副本之间的双向同步（两个方向各一次）。
func SyncBoth(a, b *Replica) (ab, ba []DiffEntry, err error) {
	return nil, nil, nil
}
