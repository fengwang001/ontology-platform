// Package frames 是定长环形帧缓冲，帧号从 1 连续递增，只保留最近 K 帧。
package frames

// Record 是一帧的不可变记录。
type Record struct {
	Tick int64  // 帧号，从 1 开始
	Now  int64  // 该帧追加时的逻辑时钟（毫秒）
	Size uint64 // 该帧字节数
	Sum  uint64 // 从第 1 帧到本帧（含）的字节数前缀和
}

// Ring 是定长环形帧缓冲。零值不可用，须用 New 构造。
type Ring struct {
	capacity int
	slots    []Record // 长度为 K，逻辑帧号 tick 落在 slot (tick-1)%K
	cur      int64
	evicted  uint64 // 已被淘汰帧（1..low-1）的 size 总和
	touched  int
}

// New 构造容量为 K 的环形缓冲。
func New(k int) *Ring {
	return &Ring{capacity: k, slots: make([]Record, k)}
}

// Append 追加下一帧，返回该帧记录。
func (r *Ring) Append(now int64, size uint64) Record {
	tick := r.cur + 1
	var sum uint64
	if r.cur > 0 {
		sum = r.at(r.cur).Sum
	}
	rec := Record{Tick: tick, Now: now, Size: size, Sum: sum + size}
	idx := (tick - 1) % int64(r.capacity)
	if tick > int64(r.capacity) {
		r.evicted = r.slots[idx].Sum
	}
	r.slots[idx] = rec
	r.cur = tick
	return rec
}

// Cur 返回最新帧号，尚无帧为 0。
func (r *Ring) Cur() int64 { return r.cur }

// Low 返回缓冲内最旧帧号 low=max(1, cur-K+1)。
func (r *Ring) Low() int64 {
	if r.cur == 0 {
		return 1
	}
	low := r.cur - int64(r.capacity) + 1
	if low < 1 {
		return 1
	}
	return low
}

// at 是不计触碰的内部点读，tick 必须仍在缓冲内。
func (r *Ring) at(tick int64) Record {
	return r.slots[(tick-1)%int64(r.capacity)]
}

// Read 点读某一帧（供 resume 规划器使用，计入 touched）。
func (r *Ring) Read(tick int64) (Record, bool) {
	r.touched++
	if tick < r.Low() || tick > r.cur {
		return Record{}, false
	}
	return r.at(tick), true
}

// Window 返回区间 [from,to]（含）内各帧 size 之和。
// 供帧落库时计算快照大小，内部维护用途，不计入 touched。
func (r *Ring) Window(from, to int64) uint64 {
	if from > to {
		return 0
	}
	var head uint64
	if from > 1 {
		if from-1 < r.Low() {
			head = r.evicted
		} else {
			head = r.at(from - 1).Sum
		}
	}
	return r.at(to).Sum - head
}

// EvictedPrefix 返回已被淘汰帧（1..low-1）的 size 总和。
func (r *Ring) EvictedPrefix() uint64 { return r.evicted }

// Touched 返回自上次 ResetTouched 以来 Read 的调用次数。
func (r *Ring) Touched() int { return r.touched }

// ResetTouched 清零 touched 计数器。
func (r *Ring) ResetTouched() { r.touched = 0 }
