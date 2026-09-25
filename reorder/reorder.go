// Package reorder 实现有界乱序重放缓冲器（最小堆/级联/超时 flush），仅依赖 seq；非导出 cmp 不计入公开接口。
package reorder

import (
	"container/heap"
	"errors"
	"reflect"
	"sort"

	"ontology/seq"
)

var ErrOverflow, ErrBadParams = errors.New("reorder: 缓冲溢出"), errors.New("reorder: 参数非法") // 四类哨兵之二，另两类在 seq

type minHeap struct {
	ev  []seq.Event
	cmp *int
}

func (h minHeap) Len() int           { return len(h.ev) }
func (h minHeap) Less(i, j int) bool { *h.cmp++; return h.ev[i].Seq < h.ev[j].Seq }
func (h minHeap) Swap(i, j int)      { h.ev[i], h.ev[j] = h.ev[j], h.ev[i] }
func (h *minHeap) Push(x any)        { h.ev = append(h.ev, x.(seq.Event)) }
func (h *minHeap) Pop() any          { x := h.ev[len(h.ev)-1]; h.ev = h.ev[:len(h.ev)-1]; return x }

type Buffer struct {
	next, now, lastAdvance, timeout int64
	maxBuffered                     int
	pq                              minHeap
	emitted                         []seq.Event
	lost                            *seq.Lost
	cmp                             int
}

func New(maxBuffered, timeout int) (*Buffer, error) { // maxBuffered<1 或 timeout<0 整体失败
	if maxBuffered < 1 || timeout < 0 {
		return nil, ErrBadParams
	}
	b := &Buffer{next: 1, timeout: int64(timeout), maxBuffered: maxBuffered, lost: seq.NewLost()}
	b.pq.cmp = &b.cmp
	return b, nil
}
func (b *Buffer) emit(e seq.Event) { b.emitted = append(b.emitted, e); b.next++; b.lastAdvance = b.now } // 发出唯一入口
func (b *Buffer) drain(out []seq.Event) []seq.Event { // 级联发出堆顶 Seq==next 事件
	b.cmp = 0 // 入口清零，返回后 cmp 即本轮为定位各最小 Seq 而比较的条数
	for b.pq.Len() > 0 && b.pq.ev[0].Seq == b.next {
		e := heap.Pop(&b.pq).(seq.Event)
		b.emit(e)
		out = append(out, e)
	}
	return out
}
func (b *Buffer) Feed(ev seq.Event) ([]seq.Event, error) { // 三类拒绝都在写入前返回，失败不留痕
	switch seq.Classify(ev.Seq, b.next) {
	case seq.Invalid:
		return nil, seq.ErrInvalid
	case seq.Expired:
		return nil, seq.ErrExpired
	case seq.Hit:
		b.emit(ev)
		return b.drain([]seq.Event{ev}), nil
	default: // Gap：加入之前判超界
		if b.pq.Len() >= b.maxBuffered {
			return nil, ErrOverflow
		}
		heap.Push(&b.pq, ev)
		return nil, nil
	}
}
func (b *Buffer) Tick() ([]seq.Event, error) { // 缓冲非空、堆顶>next 且 now-lastAdvance>=timeout 时超时 flush
	b.now++
	if b.pq.Len() > 0 && b.pq.ev[0].Seq > b.next && b.now-b.lastAdvance >= b.timeout {
		for s := b.next; s < b.pq.ev[0].Seq; s++ {
			b.lost.Add(s)
		}
		b.next = b.pq.ev[0].Seq
		return b.drain(nil), nil
	}
	return nil, nil
}
func (b *Buffer) View() []seq.Event { return append(b.emitted[:0:0], b.emitted...) }
func (b *Buffer) Lost() []int64     { return b.lost.All() }
func bSeq(b *Buffer) []int64 { // 缓冲内 Seq 的升序拷贝（堆内部为堆序，比对前排序）
	x := make([]int64, b.pq.Len())
	for i, e := range b.pq.ev {
		x[i] = e.Seq
	}
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	return x
}

type row struct { // op>0=Feed(op)，op==0=Tick
	op, now, next, la int64
	buf, lost         []int64
	out               []seq.Event
	err               error
}

var eleven = []row{
	{1, 0, 2, 0, []int64{}, nil, []seq.Event{{Seq: 1, Value: 1}}, nil},
	{3, 0, 2, 0, []int64{3}, nil, nil, nil},
	{5, 0, 2, 0, []int64{3, 5}, nil, nil, nil},
	{0, 1, 2, 0, []int64{3, 5}, nil, nil, nil},
	{0, 2, 4, 2, []int64{5}, []int64{2}, []seq.Event{{Seq: 3, Value: 3}}, nil},
	{2, 2, 4, 2, []int64{5}, []int64{2}, nil, seq.ErrExpired},
	{4, 2, 6, 2, []int64{}, []int64{2}, []seq.Event{{Seq: 4, Value: 4}, {Seq: 5, Value: 5}}, nil},
	{8, 2, 6, 2, []int64{8}, []int64{2}, nil, nil},
	{9, 2, 6, 2, []int64{8, 9}, []int64{2}, nil, nil},
	{10, 2, 6, 2, []int64{8, 9, 10}, []int64{2}, nil, nil},
	{11, 2, 6, 2, []int64{8, 9, 10}, []int64{2}, nil, ErrOverflow},
}

// SelfCheck 对第三节十一步逐字段核验内部状态、四不变量与比较数复杂度；只回 error、不外泄 cmp、不碰共享状态，可并发。
func SelfCheck() error {
	b, _ := New(3, 2)
	for _, r := range eleven {
		var out []seq.Event
		var err error
		if r.op == 0 {
			out, err = b.Tick()
		} else {
			out, err = b.Feed(seq.Event{Seq: r.op, Value: int(r.op)})
		}
		if !errors.Is(err, r.err) || b.now != r.now || b.next != r.next || b.lastAdvance != r.la ||
			!reflect.DeepEqual(bSeq(b), r.buf) || !reflect.DeepEqual(out, r.out) ||
			!reflect.DeepEqual(b.Lost(), r.lost) {
			return errors.New("十一步逐字段不符")
		}
	}
	for range [16]struct{}{} { // 再 Tick 足够多次清空：丢 6,7、发 8,9,10
		b.Tick()
	}
	want := []seq.Event{{Seq: 1, Value: 1}, {Seq: 3, Value: 3}, {Seq: 4, Value: 4}, {Seq: 5, Value: 5}, {Seq: 8, Value: 8}, {Seq: 9, Value: 9}, {Seq: 10, Value: 10}}
	if !reflect.DeepEqual(want, b.View()) || !reflect.DeepEqual(b.Lost(), []int64{2, 6, 7}) {
		return errors.New("朴素重放不一致或丢失集合错")
	}
	return checkCompares()
}
func checkCompares() error { // 同包内读非导出 cmp：一次 flush 只发堆顶 3，比较条数不随 m 线性增长
	for _, m := range []int{100, 1000, 10000} {
		b, _ := New(m+1, 1)
		for j := -1; j < m; j++ { // j=-1 发 1；其后 3,5,...,2m+1：互不相邻且都大于 next
			_, _ = b.Feed(seq.Event{Seq: int64(2*j + 3), Value: 2*j + 3})
		}
		if out, _ := b.Tick(); len(out) != 1 || out[0].Seq != 3 || b.cmp == 0 || b.cmp > 32 { // timeout=1：丢2只发3
			return errors.New("比较条数随 m 线性增长或未用堆定位")
		}
	}
	return nil
}
