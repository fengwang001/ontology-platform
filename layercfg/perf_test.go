package layercfg

import (
	"fmt"
	"sync"
	"testing"
)

// TestStructuralSharing 发布后未修改的 keyState 必须与父版本共享同一指针，
// 即历史存储不因每次发布复制整个配置；受影响键则必须是新指针（写时复制）。
func TestStructuralSharing(t *testing.T) {
	s := NewStore()
	const n = 200
	for i := 0; i < n; i++ {
		if err := s.RegisterKey(SchemaSpec{Key: fmt.Sprintf("k%d", i), Type: TypeString}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Publish([]Change{
		{Op: OpSetValue, Ref: Ref{Layer: LayerGlobal}, Key: "k0", Value: NewString("v")},
	}); err != nil {
		t.Fatal(err)
	}
	v1 := s.history[1]

	// 第二次发布只改 k1。
	if _, err := s.Publish([]Change{
		{Op: OpSetValue, Ref: Ref{Layer: LayerGlobal}, Key: "k1", Value: NewString("v2")},
	}); err != nil {
		t.Fatal(err)
	}
	v2 := s.history[2]

	shared := 0
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%d", i)
		a, b := v1.keys[key], v2.keys[key]
		if key == "k1" {
			if a == b {
				t.Fatalf("changed key %s must be copied (COW)", key)
			}
			continue
		}
		if a != nil {
			if a != b {
				t.Fatalf("untouched existing key %s must share pointer", key)
			}
			shared++
		}
	}
	if shared != 1 { // 仅 k0 是已存在但本次未改的键
		t.Fatalf("expected exactly 1 shared existing keyState (k0), got %d", shared)
	}
}

// TestConcurrentSerializability 并发发布与解析：解析只应看到完整版本，版本号严格连续。
func TestConcurrentSerializability(t *testing.T) {
	s := NewStore()
	if err := s.RegisterKey(SchemaSpec{Key: "v", Type: TypeInt,
		Range: &IntRange{Min: 0, Max: 1 << 30}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish([]Change{{
		Op: OpSetValue, Ref: Ref{Layer: LayerGlobal}, Key: "v", Value: NewInt(0),
	}}); err != nil {
		t.Fatal(err)
	}
	const writers, readers, rounds = 8, 8, 100
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				_, _ = s.Publish([]Change{{
					Op:    OpSetValue,
					Ref:   Ref{Layer: LayerGlobal},
					Key:   "v",
					Value: NewInt(int64(id*rounds + r)),
				}})
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds*writers; i++ {
				res, err := s.Resolve("v", Scope{}, -1)
				if err != nil {
					t.Errorf("resolve: %v", err)
					return
				}
				// 任何观察到的值都必须是某次完整发布写入的整数。
				if !res.Present || res.Value.Type != TypeInt {
					t.Errorf("observed torn state: %+v", res)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := s.Current(), 1+writers*rounds; got != want {
		t.Fatalf("current = %d, want exactly %d successful serial versions", got, want)
	}
	// 历史链连续无空洞。
	for v := 0; v <= 1+writers*rounds; v++ {
		if s.history[v].version != v {
			t.Fatalf("history gap at %d", v)
		}
	}
}

// BenchmarkResolveScaling 单次解析开销应与总键数、版本数无关：
// 大幅增加键数与历史版本后，解析单键耗时保持在同一量级。
func BenchmarkResolveScaling(b *testing.B) {
	measure := func(keys, versions int) func(b *testing.B) {
		return func(b *testing.B) {
			s := NewStore()
			for i := 0; i < keys; i++ {
				if err := s.RegisterKey(SchemaSpec{Key: fmt.Sprintf("k%d", i), Type: TypeString}); err != nil {
					b.Fatal(err)
				}
			}
			for v := 0; v < versions; v++ {
				if _, err := s.Publish([]Change{{
					Op: OpSetValue, Ref: Ref{Layer: LayerGlobal},
					Key: fmt.Sprintf("k%d", v%keys), Value: NewString(fmt.Sprintf("v%d", v)),
				}}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = s.Resolve("k0", Scope{Env: "e", Region: "r", Instance: "i"}, -1)
			}
		}
	}
	b.Run("small", measure(50, 50))
	b.Run("large", measure(2000, 2000))
}

// BenchmarkPublishSharesHistory 发布只应复制受影响键，基准观测分配不随键总数线性增长。
func BenchmarkPublishSharesHistory(b *testing.B) {
	for _, keys := range []int{100, 4000} {
		keys := keys
		b.Run(fmt.Sprintf("keys=%d", keys), func(b *testing.B) {
			s := NewStore()
			for i := 0; i < keys; i++ {
				if err := s.RegisterKey(SchemaSpec{Key: fmt.Sprintf("k%d", i), Type: TypeString}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = s.Publish([]Change{{
					Op: OpSetValue, Ref: Ref{Layer: LayerGlobal},
					Key: "k0", Value: NewString("x"),
				}})
			}
		})
	}
}
