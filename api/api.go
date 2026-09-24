// Package api 是对外门面：构造、版本变更、事件喂入、水位线、输出与自检。
package api

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"ontology/tjoin"
)

type Joined = tjoin.Joined // 一条连接输出
type Engine struct{ j *tjoin.Joiner }
type nver struct {
	val  string
	tomb bool
}
type op struct {
	kind     int // 0 upsert, 1 delete, 2 feed, 3 watermark, 4 flush
	key, val string
	arg      int64
	wantErr  error
}

var (
	ErrEmptyKey = tjoin.ErrEmptyKey
	ErrLate     = tjoin.ErrLate
	ErrWmBack   = tjoin.ErrWmBack
	ErrBufFull  = tjoin.ErrBufFull
)

func New(maxBuffered int) *Engine                           { return &Engine{j: tjoin.New(maxBuffered)} }
func (e *Engine) Upsert(k string, vf int64, v string) error { return e.j.Upsert(k, vf, v) }
func (e *Engine) Delete(k string, vf int64) error           { return e.j.Delete(k, vf) }
func (e *Engine) Feed(k string, ts int64) ([]Joined, error) { return e.j.Feed(k, ts) }
func (e *Engine) Watermark(w int64) ([]Joined, error)       { return e.j.Watermark(w) }
func (e *Engine) Flush() []Joined                           { return e.j.Flush() }
func (e *Engine) Outputs() []Joined                         { return e.j.Outputs() } // 迄今全部输出
func naiveAsOf(m map[int64]nver, ts int64) (string, bool) {
	best, val, tomb := int64(math.MinInt64), "", true
	for vf, v := range m {
		if vf <= ts && vf >= best {
			best, val, tomb = vf, v.val, v.tomb
		}
	}
	if tomb {
		return "", false
	}
	return val, true
}
func run(seq []op, maxBuf int) error {
	e := New(maxBuf)
	naive := map[string]map[int64]nver{}
	var events []op
	var prevOuts []Joined
	vwm := int64(math.MinInt64)
	act := []func(op) ([]Joined, error){
		func(o op) ([]Joined, error) { return nil, e.Upsert(o.key, o.arg, o.val) },
		func(o op) ([]Joined, error) { return nil, e.Delete(o.key, o.arg) },
		func(o op) ([]Joined, error) { return e.Feed(o.key, o.arg) },
		func(o op) ([]Joined, error) { return e.Watermark(o.arg) },
		func(o op) ([]Joined, error) { vwm = math.MaxInt64; return e.Flush(), nil },
	}
	for _, o := range seq {
		batch, err := act[o.kind](o)
		if !errors.Is(err, o.wantErr) { // wantErr==nil 时要求 err==nil
			return fmt.Errorf("op %+v: want %v, got %v", o, o.wantErr, err)
		}
		outs := e.Outputs()
		if len(outs) < len(prevOuts) || !slices.Equal(outs[:len(prevOuts)], prevOuts) {
			return fmt.Errorf("emitted outputs mutated") // 不变量3：已输出不可变
		}
		if o.wantErr != nil {
			if len(outs) != len(prevOuts) {
				return fmt.Errorf("rejected op changed outputs") // 不变量4：失败不留痕
			}
			continue
		}
		prevOuts = outs
		if o.kind <= 1 {
			if naive[o.key] == nil {
				naive[o.key] = map[int64]nver{}
			}
			naive[o.key][o.arg] = nver{o.val, o.kind == 1}
		} else if o.kind == 2 {
			events = append(events, o)
		} else if o.kind == 3 {
			vwm = o.arg
		}
		for i, out := range batch { // 不变量2：TS<=vwm，批内 (TS,Seq) 严格升序
			if out.TS > vwm || i > 0 && (batch[i-1].TS > out.TS || batch[i-1].TS == out.TS && batch[i-1].Seq >= out.Seq) {
				return fmt.Errorf("bad emit: %v", batch)
			}
		}
	}
	outs := e.Outputs() // 不变量1：恰好一次 + 与朴素参照一致
	if len(outs) != len(events) {
		return fmt.Errorf("outputs %d != events %d", len(outs), len(events))
	}
	sorted := slices.Clone(outs)
	slices.SortFunc(sorted, func(a, b Joined) int { return a.Seq - b.Seq })
	for i, out := range sorted {
		if out.Seq != i { // seq 必须恰好是 0..n-1 各一次
			return fmt.Errorf("seq %d missing/duplicate", i)
		}
		ev := events[i]
		val, found := naiveAsOf(naive[ev.key], ev.arg)
		if out.Key != ev.key || out.TS != ev.arg || out.Value != val || out.Found != found {
			return fmt.Errorf("seq %d: got %+v, naive (%q,%v)", i, out, val, found)
		}
	}
	return nil
}

func SelfCheck() error {
	seq1 := []op{ // 第三节十三步（New(3)）+ 注入其余三类错误 + Flush
		{0, "k", "A", 10, nil}, {2, "k", "", 12, nil}, {0, "k", "B", 20, nil}, {2, "k", "", 25, nil},
		{3, "", "", 11, nil}, {3, "", "", 10, ErrWmBack}, {0, "", "Z", 15, ErrEmptyKey},
		{0, "k", "C", 12, nil}, {2, "k", "", 22, nil}, {1, "k", "", 22, nil}, {2, "k", "", 23, ErrBufFull},
		{3, "", "", 22, nil}, {0, "k", "X", 22, ErrLate}, {2, "k", "", 8, nil}, {0, "k", "D", 30, nil},
		{3, "", "", 30, nil}, {4, "", "", 0, nil},
	}
	rng := uint64(20260924)
	next := func() uint64 { rng = rng*6364136223846793005 + 1442695040888963407; return rng >> 33 }
	var seq2 []op
	vwm := int64(0)
	for i := 0; i < 300; i++ { // 随机序列：三 Key、版本变更恒在 vwm 之后、事件时间任意
		key := string(rune('a' + next()%3))
		switch next() % 3 {
		case 0:
			seq2 = append(seq2, op{0, key, fmt.Sprint(i), vwm + 1 + int64(next()%200), nil})
		case 1:
			seq2 = append(seq2, op{2, key, "", int64(next() % 300), nil})
		default:
			vwm += 1 + int64(next()%10)
			seq2 = append(seq2, op{3, "", "", vwm, nil})
		}
	}
	seq2 = append(seq2, op{4, "", "", 0, nil})
	for i, s := range []struct {
		seq    []op
		maxBuf int
	}{{seq1, 3}, {seq2, 1000}} {
		if err := run(s.seq, s.maxBuf); err != nil {
			return fmt.Errorf("seq%d: %w", i+1, err)
		}
	}
	return nil
}
