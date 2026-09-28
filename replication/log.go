package replication

// Entry 是一条日志记录，携带写入它时的领导者世代。
type Entry struct {
	Generation uint64
	Value      string
}

// GenerationStart 记录某个世代在本地日志中的起始位点。
type GenerationStart struct {
	Generation  uint64
	StartOffset uint64
}
