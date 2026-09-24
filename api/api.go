// Package api 跨阈值报警流对外接口。依赖 alm。
package api

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"

	"ontology/alm"
	"ontology/thr"
)

// 可判定哨兵错误（互不相同），自 alm 再导出。
var (
	ErrBadParam      = alm.ErrBadParam
	ErrBadHysteresis = alm.ErrBadHysteresis
	ErrEmptyKey      = alm.ErrEmptyKey
	ErrOverflow      = alm.ErrOverflow
	ErrTooManyKeys   = alm.ErrTooManyKeys
)

// Event 是一条报警事件。
type Event = alm.Event

// 事件类型再导出，便于调用方判定。
const (
	On  = thr.On
	Off = thr.Off
)

// A 是报警流实例，并发安全。
type A struct{ s *alm.Store }

// New 创建实例；T<=0 或 H<=0 报 ErrBadParam，H>=T 报 ErrBadHysteresis。
func New(t, h int64, maxKeys int) (*A, error) {
	s, err := alm.New(t, h, maxKeys)
	if err != nil {
		return nil, err
	}
	return &A{s: s}, nil
}

// Add 增减 key 的累计值，返回本次产生的事件（0 或 1 条）。
func (a *A) Add(key string, delta int64) ([]Event, error) { return a.s.Add(key, delta) }

// View 返回各 Key 当前累计值。
func (a *A) View() map[string]int64 { return a.s.View() }

// Alarm 返回各 Key 当前 on 状态。
func (a *A) Alarm() map[string]bool { return a.s.Alarm() }

// Events 返回全部事件，按发生顺序。
func (a *A) Events() []Event { return a.s.Events() }

type step struct {
	k string
	d int64
}

// naive 朴素参照：从 0 起步逐步按规则判定，独立实现用于对拍。
func naive(t, h int64, steps []step) (map[string]int64, map[string]bool, []Event) {
	val := map[string]int64{}
	on := map[string]bool{}
	var evs []Event
	for _, st := range steps {
		v := val[st.k] + st.d
		val[st.k] = v
		switch {
		case !on[st.k] && v >= t:
			on[st.k] = true
			evs = append(evs, Event{Key: st.k, Type: On})
		case on[st.k] && v < t-h:
			on[st.k] = false
			evs = append(evs, Event{Key: st.k, Type: Off})
		}
	}
	return val, on, evs
}

// SelfCheck 对内置 Add 序列核验四条不变量，全部通过返回 nil。
// 只使用自建实例，不触碰接收者状态，可并发调用。
func (a *A) SelfCheck() error {
	const t, h = 10, 3
	steps := []step{
		{"s", 10}, {"s", 2}, {"s", -5}, {"s", 2}, {"s", -2}, {"s", 4}, {"s", -7}, {"s", 3},
		{"x", 25}, {"x", -30}, {"x", 25}, {"x", -1}, {"x", -18}, {"x", -1},
	}
	st, err := New(t, h, 100)
	if err != nil {
		return err
	}
	for _, p := range steps {
		if _, err := st.Add(p.k, p.d); err != nil {
			return err
		}
	}
	wv, wa, we := naive(t, h, steps)
	if !reflect.DeepEqual(st.View(), wv) {
		return fmt.Errorf("selfcheck: View 与朴素参照不一致: %v vs %v", st.View(), wv)
	}
	if !reflect.DeepEqual(st.Alarm(), wa) {
		return fmt.Errorf("selfcheck: Alarm 与朴素参照不一致: %v vs %v", st.Alarm(), wa)
	}
	if !slices.Equal(st.Events(), we) {
		return fmt.Errorf("selfcheck: Events 与朴素参照不一致: %v vs %v", st.Events(), we)
	}
	// 不变量 4：失败不留痕——被拒前后状态逐字段相同
	before := fmt.Sprint(st.View(), st.Alarm(), st.Events())
	if _, err := st.Add("", 1); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("selfcheck: 空 key 错误不符: %v", err)
	}
	if _, err := st.Add("s", math.MaxInt64); !errors.Is(err, ErrOverflow) {
		return fmt.Errorf("selfcheck: 溢出错误不符: %v", err)
	}
	if after := fmt.Sprint(st.View(), st.Alarm(), st.Events()); after != before {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}
