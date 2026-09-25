package chain

import (
	"sync"
	"testing"

	"ontology/ver"
)

// naive 是「逐版本线性扫描取 ts<=T 的最大者」的朴素参照实现。
func naive(vs []ver.Version, T int64) (string, bool) {
	best := -1
	for i, v := range vs {
		if v.TS <= T && (best == -1 || v.TS > vs[best].TS) {
			best = i
		}
	}
	if best == -1 || vs[best].Del {
		return "", false
	}
	return vs[best].Value, true
}

func TestAsOfMatchesNaive(t *testing.T) {
	type step struct {
		v ver.Version
	}
	cases := []struct {
		name  string
		steps []step
		// 乱序插入后各 T 的期望与朴素实现一致，由循环生成查询点。
		Ts []int64
	}{
		{"乱序值+墓碑", []step{
			{ver.ValueVersion(10, "a")},
			{ver.ValueVersion(30, "b")},
			{ver.ValueVersion(20, "c")},
			{ver.Tombstone(40)},
			{ver.ValueVersion(25, "d")},
		}, []int64{0, 9, 10, 15, 20, 24, 25, 29, 30, 39, 40, 50}},
		{"墓碑后复活", []step{
			{ver.ValueVersion(1, "x")},
			{ver.Tombstone(2)},
			{ver.ValueVersion(3, "y")},
		}, []int64{0, 1, 2, 3, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Chain{}
			for _, s := range tc.steps {
				c.Insert(s.v)
			}
			for _, T := range tc.Ts {
				got, ok := c.AsOf(T)
				want, wok := naive(c.vs, T)
				if ok != wok || ok && got.Value != want {
					t.Errorf("AsOf(%d)=(%q,%v)，朴素=(%q,%v)", T, got.Value, ok, want, wok)
				}
			}
		})
	}
}

func TestImmutability(t *testing.T) {
	c := &Chain{}
	c.Insert(ver.ValueVersion(10, "a"))
	c.Insert(ver.ValueVersion(30, "b"))
	before := c.Snapshot()
	c.Insert(ver.Tombstone(40))
	c.Insert(ver.ValueVersion(20, "c"))
	after := c.Snapshot()
	for _, b := range before { // 既有版本的 ts/值/墓碑位不得变化
		var found *ver.Version
		for i := range after {
			if after[i].TS == b.TS {
				found = &after[i]
			}
		}
		if found == nil || found.Value != b.Value || found.Del != b.Del {
			t.Errorf("版本 %d 在新插入后被改动", b.TS)
		}
	}
	if s := c.Snapshot(); s[0] != (ver.Version{TS: 10, Value: "a"}) {
		t.Errorf("链序错误：%+v", s)
	}
}

func TestComparesDoNotGrowWithM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := &Chain{}
		for ts := 1; ts <= m; ts++ {
			c.Insert(ver.ValueVersion(int64(ts), "v"))
		}
		for _, T := range []int64{1, int64(m / 2), int64(m)} {
			if _, ok := c.AsOf(T); !ok {
				t.Fatalf("m=%d T=%d: 应存在", m, T)
			}
			if c.compared.Load() > 64 {
				t.Errorf("m=%d T=%d: 比较 %d 次，疑似整链扫描", m, T, c.compared.Load())
			}
		}
	}
}

func TestConcurrentAsOf(t *testing.T) {
	c := &Chain{}
	for ts := 1; ts <= 500; ts++ {
		c.Insert(ver.ValueVersion(int64(ts), "v"))
	}
	const N = 32
	var wg sync.WaitGroup
	results := make([]struct {
		s string
		b bool
	}, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		g := g
		go func() {
			defer wg.Done()
			v, ok := c.AsOf(250)
			results[g].s, results[g].b = v.Value, ok
		}()
	}
	wg.Wait()
	for _, r := range results {
		if !r.b || r.s != "v" {
			t.Fatalf("并发 AsOf 结果不一致：%+v", r)
		}
	}
}
