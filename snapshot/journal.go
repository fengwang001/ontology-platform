package snapshot

import "sync"

// Journal 是写入接受日志：所有写入在此获得全局唯一的 LSN，
// 日志顺序即写入被接受的先后顺序。Journal 是并发交互线性化
// 的支点：所有公开操作都在同一把互斥锁下完成，因此任意并发
// 交错都等价于按某一全局顺序串行处理。
type Journal struct {
	mu     sync.Mutex
	writes []Write // 下标 i 对应 LSN i+1
}

// NewJournal 返回一个空日志。
func NewJournal() *Journal {
	return &Journal{}
}

// AppendTransaction 将一笔事务的全部写入作为连续区间追加到日志，
// 返回分配给它们的 LSN 区间 [from, to]。
func (j *Journal) AppendTransaction(txn TxnID, batch []WriteSpec) (from, to LSN) {
	j.mu.Lock()
	defer j.mu.Unlock()
	from = LSN(len(j.writes)) + 1
	for i, spec := range batch {
		spec.Txn = txn
		j.writes = append(j.writes, Write{
			LSN:    from + LSN(i),
			Txn:    txn,
			Kind:   spec.Kind,
			Object: spec.Object,
			Link:   spec.Link,
			Action: spec.Action,
		})
	}
	return from, LSN(len(j.writes))
}

// AppendRaw 追加单条写入。若 spec.Txn 为空，日志为其分配一个
// 隐式的唯一事务号。该入口允许调用方构造任意到达顺序（包括
// 违反原子性或引用完整性要求的顺序），是否可导出由核对器判定。
func (j *Journal) AppendRaw(spec WriteSpec) LSN {
	j.mu.Lock()
	defer j.mu.Unlock()
	if spec.Txn == "" {
		spec.Txn = TxnID("implicit-" + itoa(uint64(len(j.writes)+1)))
	}
	lsn := LSN(len(j.writes)) + 1
	j.writes = append(j.writes, Write{
		LSN:    lsn,
		Txn:    spec.Txn,
		Kind:   spec.Kind,
		Object: spec.Object,
		Link:   spec.Link,
		Action: spec.Action,
	})
	return lsn
}

// Tip 返回当前已接受写入的最高 LSN；空日志返回 0。
func (j *Journal) Tip() LSN {
	j.mu.Lock()
	defer j.mu.Unlock()
	return LSN(len(j.writes))
}

// Entries 返回 LSN 区间 (after, upto] 内全部写入的副本。
func (j *Journal) Entries(after, upto LSN) []Write {
	j.mu.Lock()
	defer j.mu.Unlock()
	if upto > LSN(len(j.writes)) {
		upto = LSN(len(j.writes))
	}
	if after >= upto {
		return nil
	}
	out := make([]Write, upto-after)
	copy(out, j.writes[after:upto])
	return out
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
