package ontology

// Merge 把两个同参数过滤器合并为一个新过滤器：结果等价于把
// 两批元素插入同一个过滤器（位数组为两侧按位或）。a 与 b 的
// (m, k) 必须完全相同，否则返回 *ParamMismatchError（可用
// errors.Is(err, ErrParamMismatch) 判定），其中携带双方的
// (m, k)。Merge 不会修改 a 与 b。
func Merge(a, b *Filter) (*Filter, error) {
	if a.m != b.m || a.k != b.k {
		return nil, &ParamMismatchError{AM: a.m, AK: a.k, BM: b.m, BK: b.k}
	}
	aw := a.snapshotWords()
	bw := b.snapshotWords()
	merged := make([]uint64, len(aw))
	for i := range aw {
		merged[i] = aw[i] | bw[i]
	}
	return &Filter{m: a.m, k: a.k, words: merged}, nil
}

// snapshotWords 在读锁下拷贝位数组，保证是某一时刻的完整快照。
func (f *Filter) snapshotWords() []uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]uint64, len(f.words))
	copy(out, f.words)
	return out
}
