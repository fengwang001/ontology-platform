// Package flow 实现加权公平队列中的单条流：权重、虚拟完成时刻 F 与包队列。
package flow

// Flow 是单条流：权重 w、虚拟完成时刻 fin、包完成时刻队列 q（升序）。
type Flow struct {
	w   int
	fin int
	q   []int
}

// New 创建一条权重为 w 的流（w >= 1，由调用方校验）。
func New(w int) *Flow { return &Flow{w: w} }

// Submit 入队一个 size 字节的包：inc=ceil(size/w)，F=max(F,v)+inc。
// 由于 F 单调不减包完成时刻自然升序，直接追加队尾。
func (f *Flow) Submit(size, v int) {
	inc := (size + f.w - 1) / f.w
	if v > f.fin {
		f.fin = v
	}
	f.fin += inc
	f.q = append(f.q, f.fin)
}

// Head 返回队头包的完成时刻；空队列返回 ok=false。
func (f *Flow) Head() (int, bool) {
	if len(f.q) == 0 {
		return 0, false
	}
	return f.q[0], true
}

// Pop 弹出队头包并返回其完成时刻；调用前须保证非空。
func (f *Flow) Pop() int {
	h := f.q[0]
	f.q = f.q[1:]
	return h
}

// Len 返回队列长度。
func (f *Flow) Len() int { return len(f.q) }

// Finish 返回该流当前虚拟完成时刻 F。
func (f *Flow) Finish() int { return f.fin }

// Ascending 报告队列内包完成时刻是否升序（自检用）。
func (f *Flow) Ascending() bool {
	for i := 1; i < len(f.q); i++ {
		if f.q[i] < f.q[i-1] {
			return false
		}
	}
	return true
}
