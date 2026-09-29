package spill

// Row 是事务追加的一行数据。Payload 对管理器不透明，按引用透传。
type Row struct {
	Payload string
}

// Block 是一次溢写产生的不可变块。
type Block struct {
	ID   uint64
	TxID uint64
	Seq  int
	Rows []Row
}

// Storage 是溢写块存储抽象。默认实现为内存存储，调用方可替换为磁盘实现。
// 写方法由 Manager 在持锁状态下串行调用；读方法需支持与追加/提交并发。
type Storage interface {
	// Write 原子写入一个块并返回写入后的总块数。
	Write(Block) (total int, err error)
	// Read 按块号升序返回给定事务的全部块；每个块的行序与写入时一致。
	Read(txID uint64) []Block
	// Delete 删除给定事务的全部块并返回被删除的块数。
	Delete(txID uint64) int
	// Len 返回当前总块数。
}

// memStorage 是默认的内存溢写存储。
type memStorage struct{}

func newMemStorage() Storage { return nil }
