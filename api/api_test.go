package api_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/entry"
)

// TestSevenStepTrace NOTES.md 第三节的七步轨迹：每步结果与最终视图为空。
func TestSevenStepTrace(t *testing.T) {
	s, _ := api.New(10)
	eq := func(now int64, want map[string]string) {
		t.Helper()
		got, err := s.View(now)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("now=%d: got %v err=%v, want %v", now, got, err, want)
		}
	}
	_ = s.Set("k1", "v1", 5)
	eq(5, map[string]string{"k1": "v1"})
	_ = s.Set("k2", "v2", 7)
	eq(7, map[string]string{"k1": "v1", "k2": "v2"})
	if v, ok := s.Get("k1", 14); !ok || v != "v1" {
		t.Fatal("步3 应命中 v1")
	}
	eq(14, map[string]string{"k1": "v1", "k2": "v2"})
	_ = s.Set("k3", "v3", 15)
	eq(15, map[string]string{"k2": "v2", "k3": "v3"}) // k1 已过期(15−5=10)
	if _, ok := s.Get("k1", 15); ok {
		t.Fatal("步5 k1 应过期删除")
	}
	if n, _ := s.Sweep(20); n != 1 {
		t.Fatalf("步6 应删 1 个(k2), 实删 %d", n)
	}
	eq(20, map[string]string{"k3": "v3"})
	if _, ok := s.Get("k3", 25); ok {
		t.Fatal("步7 k3 应过期删除")
	}
	eq(25, map[string]string{})
}

// TestNaiveConsistency 不变量 1：多种子随机操作序列，View 逐 Key 等于朴素判定。
func TestNaiveConsistency(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		s, _ := api.New(10)
		model := map[string]entry.Entry{}
		rng := rand.New(rand.NewSource(seed))
		var now int64
		for i := 0; i < 1000; i++ {
			now += int64(rng.Intn(4))
			k := fmt.Sprintf("k%d", rng.Intn(50))
			switch rng.Intn(3) {
			case 0:
				v := fmt.Sprintf("v%d", i)
				if err := s.Set(k, v, now); err != nil {
					t.Fatal(err)
				}
				model[k] = entry.Entry{Value: v, Ts: now}
			case 1:
				s.Get(k, now)
			case 2:
				_, _ = s.Sweep(now)
			}
		}
		got, _ := s.View(now)
		want := map[string]string{}
		for k, r := range model {
			if !entry.Expired(now, r.Ts, 10) {
				want[k] = r.Value
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d: got %v, want %v", seed, got, want)
		}
	}
}

// TestBoundedMemory 不变量 2：过期 Get 立即删除，Sweep 后无残留。
func TestBoundedMemory(t *testing.T) {
	s, _ := api.New(10)
	_ = s.Set("a", "1", 0)
	_ = s.Set("b", "2", 5)
	if _, ok := s.Get("a", 10); ok { // 10−0=10≥10，过期即删
		t.Fatal("过期 Get 应 ok=false")
	}
	if n, _ := s.Sweep(15); n != 1 { // b: 15−5=10≥10
		t.Fatalf("Sweep 应删 1 个, 实删 %d", n)
	}
	if v, _ := s.View(15); len(v) != 0 {
		t.Fatalf("Sweep 后残留: %v", v)
	}
}

// TestFaultInjection 不变量 3、4：三类哨兵错误互不相同，被拒后状态不变、可继续使用。
func TestFaultInjection(t *testing.T) {
	for _, ttl := range []int64{0, -1, -100} {
		if _, err := api.New(ttl); err != api.ErrBadTTL {
			t.Fatalf("New(%d): %v", ttl, err)
		}
	}
	if api.ErrBadTTL == api.ErrBackwardClock || api.ErrBackwardClock == api.ErrEmptyKey ||
		api.ErrBadTTL == api.ErrEmptyKey {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	s, _ := api.New(10)
	_ = s.Set("x", "1", 100)
	before, _ := s.View(100)
	if err := s.Set("y", "2", 99); err != api.ErrBackwardClock {
		t.Fatalf("时钟回退: %v", err)
	}
	if err := s.Set("", "3", 101); err != api.ErrEmptyKey {
		t.Fatalf("空 key: %v", err)
	}
	if _, err := s.Sweep(50); err != api.ErrBackwardClock {
		t.Fatalf("Sweep 回退: %v", err)
	}
	if after, _ := s.View(100); !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒操作改变了状态: %v -> %v", before, after)
	}
	if err := s.Set("z", "4", 101); err != nil {
		t.Fatalf("拒绝后应可继续使用: %v", err)
	}
}

// TestSelfCheck 自检方法对内置序列核验四条不变量，且可并发调用。
func TestSelfCheck(t *testing.T) {
	s, _ := api.New(10)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- s.SelfCheck() }()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
