// Package bus 在 fanout 单订阅者队列之上做多订阅者广播：逐订阅者投递，
// 某订阅者满则只 tail-drop 它自己那份，发布者永不阻塞、其它订阅者不受影响。
package bus

import (
	"errors"
	"reflect"

	"ontology/fanout"
)

// 三类可判定哨兵错误，互不相同。越界操作用 panic 承载（签名无 error 返回位），
// recover 后以 errors.Is 判定。
var (
	ErrInvalidN        = errors.New("bus: invalid subscriber count N (must be > 0)")
	ErrInvalidC        = errors.New("bus: invalid queue capacity C (must be > 0)")
	ErrSubscriberIndex = errors.New("bus: subscriber index out of range")
)

// Bus 持有 N 个彼此独立的有界队列。
type Bus struct{ subs []*fanout.Queue }

// New 先完整校验再分配任何状态：N/C 非法返回哨兵错误，不留任何状态。
func New(n, c int) (*Bus, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	if c <= 0 {
		return nil, ErrInvalidC
	}
	subs := make([]*fanout.Queue, n)
	for i := range subs {
		subs[i] = fanout.NewQueue(c)
	}
	return &Bus{subs}, nil
}

// Publish 把同一份 ev 复制投给每个订阅者；单次 Offer 为 O(1) 且绝不阻塞，
// 故某个订阅者满既不阻塞发布者，也不影响其余订阅者收事件。
func (b *Bus) Publish(ev int64) {
	for _, q := range b.subs {
		q.Offer(ev)
	}
}

func (b *Bus) queue(si int) *fanout.Queue {
	if si < 0 || si >= len(b.subs) {
		panic(ErrSubscriberIndex)
	}
	return b.subs[si]
}

func (b *Bus) Consume(si int) (int64, bool) { return b.queue(si).Poll() }
func (b *Bus) DropCount(si int) int         { return b.queue(si).Dropped() }
func (b *Bus) QueueLen(si int) int          { return b.queue(si).Len() }

// SelfCheck 在内部新建总线重放内置操作序列核验四条不变量，不改动接收者状态。
func (b *Bus) SelfCheck() bool {
	// 不变量 1/3：N=2,C=2 六步，逐前缀核验队列内容、丢弃计数与 Consume 返回。
	steps := []func(*Bus) (int64, bool){
		func(z *Bus) (int64, bool) { z.Publish(1); return 0, false },
		func(z *Bus) (int64, bool) { z.Publish(2); return 0, false },
		func(z *Bus) (int64, bool) { z.Publish(3); return 0, false },
		func(z *Bus) (int64, bool) { return z.Consume(0) },
		func(z *Bus) (int64, bool) { z.Publish(4); return 0, false },
		func(z *Bus) (int64, bool) { return z.Consume(1) },
	}
	want := []struct {
		q0, q1 []int64
		d0, d1 int
		cv     int64
		cok    bool
	}{
		{[]int64{1}, []int64{1}, 0, 0, 0, false},
		{[]int64{1, 2}, []int64{1, 2}, 0, 0, 0, false},
		{[]int64{1, 2}, []int64{1, 2}, 1, 1, 0, false},
		{[]int64{2}, []int64{1, 2}, 1, 1, 1, true},
		{[]int64{2, 4}, []int64{1, 2}, 1, 2, 0, false},
		{[]int64{2, 4}, []int64{2}, 1, 2, 1, true},
	}
	for k := 1; k <= len(steps); k++ {
		z, _ := New(2, 2)
		var cv int64
		var cok bool
		for j := 0; j < k; j++ {
			cv, cok = steps[j](z)
		}
		w := want[k-1]
		if !reflect.DeepEqual(drain(z, 0), w.q0) || !reflect.DeepEqual(drain(z, 1), w.q1) ||
			z.DropCount(0) != w.d0 || z.DropCount(1) != w.d1 || cv != w.cv || cok != w.cok {
			return false
		}
	}
	// 不变量 2：s1 持续满时 Publish 立即返回；s0 腾位后照常收到新事件，s1 只自计丢弃。
	z, _ := New(2, 1)
	z.Publish(1)
	z.Publish(2)
	if v, ok := z.Consume(0); !ok || v != 1 || z.DropCount(0) != 1 || z.DropCount(1) != 1 {
		return false
	}
	z.Publish(3)
	if v, ok := z.Consume(0); !ok || v != 3 || z.DropCount(0) != 1 || z.DropCount(1) != 2 {
		return false
	}
	if v, ok := z.Consume(1); !ok || v != 1 {
		return false
	}
	// 不变量 4：三类错误互不相同且可判定；越界被拒后状态原样、仍可正常使用。
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidN) {
		return false
	}
	if _, err := New(1, 0); !errors.Is(err, ErrInvalidC) {
		return false
	}
	z2, _ := New(1, 1)
	z2.Publish(7)
	ri := func(si int) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err, _ = r.(error)
			}
		}()
		_ = z2.QueueLen(si)
		return
	}
	for _, si := range []int{-1, 9} {
		if ri(si) != ErrSubscriberIndex {
			return false
		}
	}
	if v, ok := z2.Consume(0); !ok || v != 7 || z2.DropCount(0) != 0 {
		return false
	}
	return true
}

func drain(z *Bus, si int) []int64 {
	var out []int64
	for {
		v, ok := z.Consume(si)
		if !ok {
			return out
		}
		out = append(out, v)
	}
}
