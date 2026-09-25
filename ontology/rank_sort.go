package ontology

// indexedRow 关联一行与其在分区副本切片中的位置。

// 排序仅重排 indexedRow，不直接重排用户数据；
// 位置字段绝不参与并列决断（决断只用排序值与行 ID）。
type indexedRow struct {
	row Row
}

// sortPartition 将一个分区的行排序：

//	第一关键字：排序值（方向由 dir 决定）；
//	第二关键字：行 ID 升序（无论升降序，并列内部永远 ID 升序）。
//
// 使用比较排序且比较次数为 O(n log n)，cmp 为 nil 时退化为
// 不计费的 compareValues。
func sortPartition(rows []Row, dir Direction, cmp *CountingComparator) []indexedRow {
	return nil
}
