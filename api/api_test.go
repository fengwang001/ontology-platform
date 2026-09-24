package api_test

import (
	"errors"
	"maps"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/ev"
)

func sp(s string) *string { return &s }

// 不变量 1：全或无。
func TestAllOrNothing(t *testing.T) {
	cases := []struct {
		name  string
		batch []api.Event
		want  map[string]string // 期望视图（相对初态 {k1:1,k2:2}）
		fail  bool
	}{
		{"全部通过", []api.Event{api.Put("k3", "3", nil), api.Put("k1", "10", sp("1"))},
			map[string]string{"k1": "10", "k2": "2", "k3": "3"}, false},
		{"首条即失败", []api.Event{api.Put("k1", "9", nil)},
			map[string]string{"k1": "1", "k2": "2"}, true},
		{"末条失败前缀不落盘", []api.Event{api.Put("k3", "3", nil), api.Del("k1", nil)},
			map[string]string{"k1": "1", "k2": "2"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := api.New()
			_ = eng.ApplyBatch([]api.Event{api.Put("k1", "1", nil), api.Put("k2", "2", nil)})
			err := eng.ApplyBatch(tc.batch)
			if (err != nil) != tc.fail {
				t.Fatalf("fail=%v err=%v", tc.fail, err)
			}
			if !maps.Equal(eng.View(), tc.want) {
				t.Fatalf("view=%v want %v", eng.View(), tc.want)
			}
		})
	}
}

// 不变量 2：批内顺序语义（前序 put/del 对后续可见）。
func TestIntraBatchOrder(t *testing.T) {
	cases := []struct {
		name  string
		batch []api.Event
		want  string // k 的最终值
	}{
		{"删后重建", []api.Event{api.Put("k", "1", nil), api.Del("k", sp("1")), api.Put("k", "2", nil)}, "2"},
		{"链式期望", []api.Event{api.Put("k", "1", nil), api.Put("k", "2", sp("1")), api.Put("k", "3", sp("2"))}, "3"},
		{"建后即删", []api.Event{api.Put("k", "1", nil), api.Del("k", sp("1")), api.Put("k", "9", nil)}, "9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := api.New()
			if err := eng.ApplyBatch(tc.batch); err != nil {
				t.Fatal(err)
			}
			if got := eng.View()["k"]; got != tc.want {
				t.Fatalf("k=%s want %s", got, tc.want)
			}
		})
	}
}

// 不变量 3：与朴素逐事件应用器对拍（随机批序列，循环生成）。
func TestMatchesNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 20; trial++ {
		eng := api.New()
		naive := map[string]string{}
		for b := 0; b < 50; b++ {
			n := 1 + rng.IntN(5)
			batch := make([]api.Event, n)
			for i := range batch {
				k := "k" + strconv.Itoa(rng.IntN(6))
				v := strconv.Itoa(rng.IntN(4))
				var exp *string
				switch rng.IntN(3) {
				case 0:
					exp = nil
				case 1:
					exp = sp(v)
				}
				if rng.IntN(4) == 0 {
					batch[i] = api.Del(k, exp)
				} else {
					batch[i] = api.Put(k, v, exp)
				}
			}
			if err := eng.ApplyBatch(batch); err == nil {
				for _, e := range batch { // 朴素器只顺序应用成功批
					if e.Kind == ev.DelKind {
						delete(naive, e.Key)
					} else {
						naive[e.Key] = e.Val
					}
				}
			}
			if !maps.Equal(eng.View(), naive) {
				t.Fatalf("trial %d batch %d: view=%v naive=%v", trial, b, eng.View(), naive)
			}
		}
	}
}

// 不变量 4 + 故障注入：失败不留痕，错误对应第一条失败事件，三哨兵互异。
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		batch []api.Event
		sent  error
	}{
		{"空批", nil, api.ErrEmptyBatch},
		{"空键", []api.Event{api.Put("a", "1", nil), api.Put("", "2", nil)}, ev.ErrEmptyKey},
		{"nil期望但键已存在", []api.Event{api.Put("a", "1", nil), api.Put("a", "2", nil)}, ev.ErrExpectation},
		{"带值期望但值不同", []api.Event{api.Put("a", "1", nil), api.Put("b", "2", sp("x"))}, ev.ErrExpectation},
		{"带值期望但键不存在", []api.Event{api.Del("ghost", sp("x"))}, ev.ErrExpectation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := api.New()
			_ = eng.ApplyBatch([]api.Event{api.Put("keep", "v", nil)})
			before := eng.View()
			err := eng.ApplyBatch(tc.batch)
			if !errors.Is(err, tc.sent) {
				t.Fatalf("err=%v want sentinel %v", err, tc.sent)
			}
			for _, other := range []error{api.ErrEmptyBatch, ev.ErrEmptyKey, ev.ErrExpectation} {
				if other != tc.sent && errors.Is(err, other) {
					t.Fatalf("err=%v unexpectedly matches %v", err, other)
				}
			}
			if !maps.Equal(eng.View(), before) {
				t.Fatalf("view mutated: %v -> %v", before, eng.View())
			}
			if err := eng.ApplyBatch([]api.Event{api.Put("after", "ok", nil)}); err != nil {
				t.Fatalf("engine unusable after rejection: %v", err)
			}
		})
	}
}

// 并发：读到的视图必须对应某个完整批次边界。
func TestConcurrentViewConsistency(t *testing.T) {
	eng := api.New()
	var stop atomic.Bool
	boundary := atomic.Bool{}
	boundary.Store(true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				w := eng.View()
				if w["ga"] != w["gb"] {
					boundary.Store(false)
				}
			}
		}()
	}
	for n := 0; n < 300; n++ {
		s := strconv.Itoa(n)
		var exp *string
		if n > 0 {
			exp = sp(strconv.Itoa(n - 1))
		}
		_ = eng.ApplyBatch([]api.Event{api.Put("ga", s, exp), api.Put("gb", s, exp)})
	}
	stop.Store(true)
	wg.Wait()
	if !boundary.Load() {
		t.Fatal("observed a half-applied batch")
	}
	if eng.View()["ga"] != "299" {
		t.Fatalf("final=%v", eng.View())
	}
}
