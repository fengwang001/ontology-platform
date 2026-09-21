package bloom

// Add 把元素加入过滤器。幂等：重复加入同一元素不改变位数组。
// 并发安全；已完成的 Add 对后续 MayContain 立即可见。
func (f *Filter) Add(data []byte) {
	pos := f.positions(data)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range pos {
		f.bits[p>>6] |= 1 << (p & 63)
	}
}

// MayContain 报告元素是否可能存在。无假阴性：已 Add 的元素
// 一定返回 true；未加入的元素以约 p 的概率误报 true。
func (f *Filter) MayContain(data []byte) bool {
	pos := f.positions(data)
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range pos {
		if f.bits[p>>6]&(1<<(p&63)) == 0 {
			return false
		}
	}
	return true
}

// Bytes 导出位数组快照（大端序，每 64 位 8 字节）。
// 返回的是某一时刻的完整副本，并发更新不会造成半更新视图；
// 调用方修改返回值不影响过滤器。
func (f *Filter) Bytes() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]byte, len(f.bits)*8)
	for i, w := range f.bits {
		for j := 0; j < 8; j++ {
			out[i*8+j] = byte(w >> (56 - 8*j))
		}
	}
	return out
}
