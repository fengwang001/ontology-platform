// Package slotring 是按序号定位槽位的底层环形数组，自身不判断覆盖。
package slotring

// Ring 为定容环形数组。序号是全局单调序号，与槽位的映射为 (seq-1)%N。
type Ring struct {
	n        int
	slots    []any
	versions []int64
}

// New 创建容量为 n 的环形数组。
func New(n int) *Ring {
	if n <= 0 {
		return nil
	}
	return &Ring{n: n, slots: make([]any, n), versions: make([]int64, n)}
}

// Cap 返回槽位容量。
func (r *Ring) Cap() int { return r.n }

// Slot 返回序号 seq 对应的槽位下标。
func (r *Ring) Slot(seq int64) int { return int((seq - 1) % int64(r.n)) }

// Put 把值写入序号 seq 的槽位，并记录该槽位当前承载的序号。
func (r *Ring) Put(seq int64, v any) {
	i := r.Slot(seq)
	r.slots[i] = v
	r.versions[i] = seq
}

// Get 读取序号 seq 对应槽位的值。调用方须自行确认该序号仍在环内。
func (r *Ring) Get(seq int64) (any, bool) {
	i := r.Slot(seq)
	if r.versions[i] != seq {
		return nil, false
	}
	return r.slots[i], true
}
