package ontology

// Rank 按分区计算 ROW_NUMBER / RANK / DENSE_RANK。

// 结果为全新分配的切片，按“分区键字典序、分区内排名顺序”排列；
// 输入切片与其元素在调用后保持逐字段不变。
// nil 分区键与 NaN 排序值的行不参与排名，计数写入返回的 Stats。
func Rank(rows []Row, order Order) ([]RankedRow, Stats) {
	return nil, Stats{}
}
