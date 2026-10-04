package ingest

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomVsModel 1500 组随机操作序列与逐序号朴素模型全状态对照，
// 失败时打印输入日志、输出与判定依据。
func TestRandomVsModel(t *testing.T) {
	const groups = 1500
	rng := rand.New(rand.NewSource(20241004))
	for g := 0; g < groups; g++ {
		cfg := Config{
			Lm: int64(1 + rng.Intn(6)),
			K:  1 + rng.Intn(4),
			Tq: int64(1 + rng.Intn(12)),
			R:  1 + rng.Intn(3),
		}
		r := NewRegistry()
		dev := fmt.Sprintf("dev%d", g)
		if err := r.Register(dev, cfg); err != nil {
			t.Fatal(err)
		}
		m := newModel(cfg)
		now := int64(0)
		var log []string
		for step := 0; step < 60; step++ {
			now += int64(rng.Intn(6))
			ms := m.stats()
			op := rng.Intn(10)
			switch {
			case op < 5:
				var seq int64
				if ms.Hi == 0 || rng.Intn(2) == 0 {
					seq = ms.Hi + int64(1+rng.Intn(10))
				} else {
					seq = int64(1 + rng.Intn(int(ms.Hi)))
				}
				if err := r.Ingest(dev, seq, now); err != nil {
					t.Fatalf("g%d Ingest err %v\n日志: %v", g, err, log)
				}
				m.ingest(seq, now)
				log = append(log, fmt.Sprintf("Ingest(seq=%d,now=%d)", seq, now))
			case op < 8:
				budget := int64(1 + rng.Intn(15))
				got, err := r.Plan(dev, now, budget)
				if err != nil {
					t.Fatalf("g%d Plan err %v\n日志: %v", g, err, log)
				}
				want := m.plan(now, budget)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("g%d step%d Plan(now=%d,budget=%d) got=%v want=%v\ncfg=%+v\n日志: %v",
						g, step, now, budget, got, want, cfg, log)
				}
				log = append(log, fmt.Sprintf("Plan(now=%d,budget=%d)=%v", now, budget, got))
			default:
				st := ms
				lo2 := st.Lo
				if rng.Intn(2) == 0 {
					lo2 += int64(rng.Intn(4))
				}
				hi2 := st.Hi
				if rng.Intn(2) == 0 {
					hi2 += int64(rng.Intn(6))
				}
				if lo2 > hi2+1 {
					lo2 = hi2 + 1
				}
				if err := r.Hello(dev, lo2, hi2, now); err != nil {
					t.Fatalf("g%d Hello(%d,%d,%d) err %v\n日志: %v", g, lo2, hi2, now, err, log)
				}
				m.hello(lo2, hi2, now)
				log = append(log, fmt.Sprintf("Hello(lo=%d,hi=%d,now=%d)", lo2, hi2, now))
			}
			// 每步全状态对照。
			got, _ := r.Stats(dev)
			want := m.stats()
			if got != want {
				t.Fatalf("g%d step%d stats 不一致\n got=%+v\nwant=%+v\ncfg=%+v\n日志: %v",
					g, step, got, want, cfg, log)
			}
			if got.Received+got.Lost+got.Missing != got.Hi {
				t.Fatalf("g%d 守恒破坏 %+v", g, got)
			}
			if g < 5 {
				t.Logf("g%d %s => %+v", g, log[len(log)-1], got)
			}
		}
	}
}
