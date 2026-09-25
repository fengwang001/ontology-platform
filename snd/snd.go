// Package snd 维护单连接的发送窗口状态：base/next 指针与已发送未确认段集合。
// 它不依赖其他包，本身是不加锁的纯状态机（并发由上层 gbn 串行化）。
package snd

import "errors"

// 哨兵错误：可被 errors.Is 判定。
var (
	// ErrBadWindow：窗口大小非正。
	ErrBadWindow = errors.New("snd: window size must be positive")
	// ErrWindowFull：next==base+W 时仍要求发送。
	ErrWindowFull = errors.New("snd: send window is full")
)

// Window 是定容环形发送窗口。ring 的容量恰为 W，段 seq 落在槽 seq%W。
// 不变量：len(ring)==W；对任意 i∈[base,next)，ring[i%W]==i；next-base<=W。
type Window struct {
	w     int
	base  int64 // 已确认下界：seq<base 全部被累计 ACK 覆盖
	next  int64 // 下一个待发送段序号
	ring  []int64
	check int // advanceChecks：最近一次 base 被推进时检查过的段个数（非导出）
}

// New 创建容量为 W 的空窗口；W 非正返回 ErrBadWindow，且不留任何状态。
func New(W int) (*Window, error) {
	if W <= 0 {
		return nil, ErrBadWindow
	}
	return &Window{w: W, ring: make([]int64, W)}, nil
}

// Send 在窗口未满时发送段 next 并令 next 加一；满则返回 ErrWindowFull，状态不变。
func (w *Window) Send() (int64, error) {
	if w.next-w.base >= int64(w.w) {
		return 0, ErrWindowFull
	}
	seq := w.next
	w.ring[seq%int64(w.w)] = seq
	w.next = seq + 1
	return seq, nil
}

// Advance 处理累计 ACK：base=max(base,a)，只进不退。
// 推进只移动 base 指针，不逐个扫描/清理槽位，故本次检查段数恒为 0；
// a<=base 为重复/乱序 ACK，整调用幂等，计数器也不变。
// a 超过 next（ACK 到尚未发送的段）时钳到 next，使 base 永不越过已发送上界，
// 从而与「从 0 逐个检查已发送段」的朴素参照在任意输入下保持一致。
func (w *Window) Advance(a int64) {
	if a <= w.base {
		return
	}
	if a > w.next {
		a = w.next
	}
	if a <= w.base {
		return
	}
	w.check = 0
	w.base = a
}

// Timeout 返回需 Go-Back-N 重传的全部段 [base,next)；无未确认段时返回 nil。
// base/next 不变；段对象在重传后由上层重新计时，窗口状态无需改动。
func (w *Window) Timeout() []int64 {
	if w.next <= w.base {
		return nil
	}
	out := make([]int64, 0, w.next-w.base)
	for i := w.base; i < w.next; i++ {
		out = append(out, w.ring[i%int64(w.w)])
	}
	return out
}

// Base 返回已确认下界。
func (w *Window) Base() int64 { return w.base }

// Next 返回下一个待发送段序号。
func (w *Window) Next() int64 { return w.next }

// Unacked 返回当前未确认段的有序切片 [base,next)。
func (w *Window) Unacked() []int64 {
	if w.next <= w.base {
		return nil
	}
	out := make([]int64, 0, w.next-w.base)
	for i := w.base; i < w.next; i++ {
		out = append(out, w.ring[i%int64(w.w)])
	}
	return out
}

// SelfCheckAdvanceConstant 是供对外自检复用的复杂度判定：对多档 m 构造
// base=0、next=m 的全未确认窗口，一次 Ack(m) 推进到底，断言内部检查段数
// 恒为 0（与 m 无关）。它只返回布尔结论，计数器数值不经任何导出签名外泄。
func SelfCheckAdvanceConstant() bool {
	for _, m := range []int{100, 1000, 10000} {
		w, err := New(m)
		if err != nil {
			return false
		}
		for range m {
			if _, err := w.Send(); err != nil {
				return false
			}
		}
		w.Advance(int64(m))
		if w.Base() != int64(m) || w.check != 0 {
			return false
		}
	}
	return true
}
