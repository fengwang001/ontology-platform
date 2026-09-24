// Package window 有界重排窗口：按序号暂存结果，只放行下一个该输出的序号。
// 暂存数上限 W 由调用方（pmap）通过派发闸门保证，本包记录历史最大暂存数以便验证。
package window

// Window 非并发安全，仅由单个调度 goroutine 使用。
type Window[T any] struct {
	next int       // 下一个该输出的序号
	buf  map[int]T // 乱序暂存
	max  int       // 历史最大暂存数
}

func New[T any]() *Window[T] { return &Window[T]{buf: make(map[int]T)} }

// Put 暂存序号 i 的结果。
func (w *Window[T]) Put(i int, v T) {
	w.buf[i] = v
	if len(w.buf) > w.max {
		w.max = len(w.buf)
	}
}

// PopReady 取出从 next 起连续可放行的结果，返回起始序号与值序列。
func (w *Window[T]) PopReady() (int, []T) {
	start := w.next
	var out []T
	for {
		v, ok := w.buf[w.next]
		if !ok {
			return start, out
		}
		delete(w.buf, w.next)
		w.next++
		out = append(out, v)
	}
}

// Next 返回下一个该输出的序号。
func (w *Window[T]) Next() int { return w.next }

// Max 返回历史最大暂存数。
func (w *Window[T]) Max() int { return w.max }
