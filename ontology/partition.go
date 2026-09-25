package ontology

// partitionGroup 保存一个分区内通过校验的行副本。

// 副本而非原指针，保证调用方输入在任何情况下都不被修改。
type partitionGroup struct {
	key  string
	rows []Row
}

// groupPartitions 将输入行按分区键分组。

// 返回的分组顺序不做要求（由 rankPartitions 负责按字典序排序）；
// nil 分区键与 NaN 排序值的行被拒绝并累计到 stats。
func groupPartitions(rows []Row, stats *Stats) []partitionGroup {
	return nil
}

// sortGroupsByKey 将分区按分区键字典序原地排序。
func sortGroupsByKey(groups []partitionGroup) {
}
