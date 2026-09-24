// Package api 是有界乱序事件时间重排缓冲的对外入口：迟到事件走旁路，主输出严格有序。
package api

import (
	"errors"
	"ontology/order"
	"ontology/rbuf"
	"slices"
	"sync"
)

// Out 是一条对外输出的事件；与 rbuf.Event 同型，直接贯穿三个包。
type Out = rbuf.Event

// 四类可判定、互不相同的哨兵错误。
var (
	ErrInvalidParam = errors.New("api: delay 不能为负且 maxBuffered 必须为正")
	ErrEmptyID      = errors.New("api: 事件 ID 不能为空串")
	ErrDuplicateID  = errors.New("api: 该 ID 已被接受过")
	ErrBufferFull   = errors.New("api: 缓冲已满（本事件释放之前达到 maxBuffered）")
)

// Reorder 是重排缓冲；零值不可用，须经 New 构造。
type Reorder struct {
	mu             sync.Mutex
	delay, wm, seq int64
	max            int
	buf            rbuf.Buffer
	hasWM          bool
	seen           map[string]struct{}
}

// New 以乱序界限 delay 与缓冲上限 maxBuffered 构造缓冲。
func New(delay int64, maxBuffered int) (*Reorder, error) {
	if delay < 0 || maxBuffered <= 0 {
		return nil, ErrInvalidParam
	}
	return &Reorder{delay: delay, max: maxBuffered, seen: map[string]struct{}{}}, nil
}

// Push 按「迟到判定 → 推进水位线 → 释放」三步处理事件，返回本次新增的主/旁路输出。
// 任何拒绝都不改变状态（含水位线与 Seq）、不占到达序号。
func (r *Reorder) Push(id string, ts int64) (main, side []Out, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" { // 三个判定全部先于任何状态修改，保证失败不留痕
		return nil, nil, ErrEmptyID
	}
	if _, dup := r.seen[id]; dup {
		return nil, nil, ErrDuplicateID
	}
	late := order.Late(ts, r.wm, r.hasWM)
	if !late && r.buf.Len() >= r.max {
		return nil, nil, ErrBufferFull // 迟到事件不进缓冲，不受此限
	}
	ev := rbuf.Event{ID: id, TS: ts, Seq: r.seq} // 接受：分配连续到达序号
	r.seq++
	r.seen[id] = struct{}{}
	if late {
		r.buf.Bypass(ev) // 步骤1：迟到立即入旁路
	} else {
		r.buf.Buffer(ev) // 步骤1：非迟到进入缓冲
	}
	r.wm, r.hasWM = order.Advance(r.wm, r.hasWM, ts, r.delay) // 步骤2：推进水位线
	if late {
		return nil, []Out{ev}, nil // 迟到不推进 wm，缓冲无事件可释放
	}
	return r.buf.Release(r.wm), nil, nil // 步骤3：按水位线释放主输出
}

// Flush 把水位线推到正无穷并释放全部缓冲；此后到达的事件一律迟到。
func (r *Reorder) Flush() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wm, r.hasWM = order.PosInf, true
	return r.buf.Flush()
}

// Main 返回迄今全部主输出（任意前缀严格按 (TS, Seq) 递增）。
func (r *Reorder) Main() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Main()
}

// Side 返回迄今全部旁路输出（保持到达顺序）。
func (r *Reorder) Side() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Side()
}

// SelfCheck 用第三节内置十事件序列核验第二节四条不变量，期望值取自 NOTES 推导表。
func (r *Reorder) SelfCheck() error {
	s, _ := New(3, 10)
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	tss := []int64{5, 3, 9, 5, 6, 9, 4, 12, 12, 16}
	for i := range ids {
		if _, _, e := s.Push(ids[i], tss[i]); e != nil {
			return e
		}
	}
	if f := s.Flush(); len(f) != 1 || f[0].ID != "j" {
		return errors.New("selfcheck: Flush 应只释放 j")
	}
	wantMain := []Out{{ID: "b", TS: 3, Seq: 1}, {ID: "a", TS: 5, Seq: 0},
		{ID: "c", TS: 9, Seq: 2}, {ID: "f", TS: 9, Seq: 5},
		{ID: "h", TS: 12, Seq: 7}, {ID: "i", TS: 12, Seq: 8},
		{ID: "j", TS: 16, Seq: 9}}
	wantSide := []Out{{ID: "d", TS: 5, Seq: 3}, {ID: "e", TS: 6, Seq: 4},
		{ID: "g", TS: 4, Seq: 6}}
	gotMain, gotSide := s.Main(), s.Side()
	if !slices.Equal(gotMain, wantMain) || !slices.Equal(gotSide, wantSide) {
		return errors.New("selfcheck: 不变量1 与朴素参照不一致")
	}
	for i := 1; i < len(gotMain); i++ {
		a, b := gotMain[i-1], gotMain[i]
		if !order.Less(order.Key{TS: a.TS, Seq: a.Seq}, order.Key{TS: b.TS, Seq: b.Seq}) {
			return errors.New("selfcheck: 不变量2 主输出非严格递增")
		}
	}
	if len(gotMain)+len(gotSide) != len(ids) {
		return errors.New("selfcheck: 不变量3 违反恰好一次")
	}
	// 不变量4：四类拒绝可判定、互不相同；被拒后状态不变且缓冲仍可用。
	full, _ := New(10, 1) // delay=10：x:100 使 wm=90，x 滞留缓冲占满名额
	full.Push("x", 100)
	_, _, eFull := full.Push("y", 101)
	_, eParam := New(-1, 1)
	_, _, eEmpty := s.Push("", 1)
	_, _, eDup := s.Push("a", 1)
	pairs := [][2]error{{eFull, ErrBufferFull}, {eParam, ErrInvalidParam},
		{eEmpty, ErrEmptyID}, {eDup, ErrDuplicateID}}
	for _, p := range pairs {
		if !errors.Is(p[0], p[1]) {
			return errors.New("selfcheck: 不变量4 拒绝错误不可判定")
		}
	}
	if !slices.Equal(s.Main(), gotMain) || !slices.Equal(s.Side(), gotSide) {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	if _, _, e := full.Push("y", 1); e != nil || len(full.Side()) != 1 {
		return errors.New("selfcheck: 超限后未保持可用或留下痕迹")
	}
	return nil
}
