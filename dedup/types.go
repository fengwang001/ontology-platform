package dedup

// Record 是一次投递中的单条记录，归属于某个分区的某个位点。
type Record struct {
	Partition int
	Offset    int64
	Key       string
	Value     int64
}

// PartitionState 是单个分区的持久状态：已生效的最大位点与累计重复条数。
type PartitionState struct {
	Watermark  int64
	Duplicates int64
}

// Result 是一次 Apply 后的判定结果。
type Result struct {
	// Applied 是本批实际累加进结果表的记录。
	Applied []Record
	// Duplicates 是本批中位点不超过水位、被丢弃的记录。
	Duplicates []Record
	// Totals 是提交后结果表中各键的累计值。
	Totals map[string]int64
	// Watermarks 是提交后各分区的水位。
	Watermarks map[int]int64
}

func zeroResult() Result { return Result{} }
