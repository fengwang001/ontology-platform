package ontology

import (
	"sync"
	"testing"
)

func validCfg() Config {
	return Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 4096,
		T: 100, W: 10, ThU: 25, ThD: 50, Kc: 2, C0: 4, Pool: 1 << 30, H: 5}
}

func TestInvalidConfigs(t *testing.T) {
	bad := []func(Config) Config{
		func(c Config) Config { c.G = 0; return c },
		func(c Config) Config { c.G = 3; return c }, // G 不整除 Bmin(1024)
		func(c Config) Config { c.Bmin = 1023; return c },
		func(c Config) Config { c.B0 = 512; return c },    // B0 < Bmin
		func(c Config) Config { c.Bmax = 3072; return c }, // Bmax < B0
		func(c Config) Config { c.Bmax = 1<<30 + 1024; return c },
		func(c Config) Config { c.Bmin = 1536; return c }, // G 不整除 Bmin
		func(c Config) Config { c.T = 0; return c },
		func(c Config) Config { c.T = 1_000_001; return c },
		func(c Config) Config { c.W = 0; return c },
		func(c Config) Config { c.W = 101; return c },
		func(c Config) Config { c.ThU = 1001; return c },
		func(c Config) Config { c.ThD = 101; return c },
		func(c Config) Config { c.Kc = 0; return c },
		func(c Config) Config { c.Kc = 101; return c },
		func(c Config) Config { c.C0 = 0; return c },
		func(c Config) Config { c.C0 = 10001; return c },
		func(c Config) Config { c.Pool = 0; return c },
		func(c Config) Config { c.Pool = 1<<40 + 1; return c },
		func(c Config) Config { c.H = -1; return c },
		func(c Config) Config { c.H = 1001; return c },
		// Beff(C0) < B0：Pool 不足以给每个通道 B0
		func(c Config) Config { c.C0 = 4; c.Pool = 3 * 4 * 1024; return c },
		// floor(Pool/(C*G))*G 非整除时也要满足
		func(c Config) Config { c.C0 = 2; c.Pool = 3*2*1024 + 511; return c },
	}
	for i, mut := range bad {
		cfg := mut(validCfg())
		if d, err := NewDeflator(cfg); err == nil {
			t.Fatalf("case %d expected rejection, got deflator beff=%d", i,
				beff(cfg.Bmax, cfg.Pool, cfg.C0, cfg.G))
			_ = d
		}
	}
	// 合法边界：Beff 恰等于 B0 应接受
	cfg := validCfg()
	cfg.C0 = 4
	cfg.Pool = 4096 * 4
	if _, err := NewDeflator(cfg); err != nil {
		t.Fatalf("beff==B0 must be accepted: %v", err)
	}
	// Pool 上限 2^40
	cfg = validCfg()
	cfg.Pool = 1 << 40
	if _, err := NewDeflator(cfg); err != nil {
		t.Fatalf("pool=2^40: %v", err)
	}
}

func Test128BitThroughput(t *testing.T) {
	// bytes=2^30, dt=1 => R=2^30*1000 ≈ 1.07e12；T=1e6 => R*T ≈ 1.07e18（>2^53）
	// 最大 R*T ≈ 1.07e12 * 1e6 = 1.07e18 < 2^64；题面允许 128 位中间值。
	cfg := Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 1024,
		T: 1_000_000, W: 1, ThU: 0, ThD: 0, Kc: 1, C0: 1, Pool: 1 << 40, H: 0}
	d, err := NewDeflator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Sample(1<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantR := uint64(1<<30) * 1000
	if r.R != wantR {
		t.Fatalf("R=%d want %d", r.R, wantR)
	}
	// Dt=ceil(R*T/1000)=R*1000 = 2^30*1e6 = 1.0737e18；cand 被 Bmax 夹到 2^30
	if r.Cand != 1<<30 || r.Cur != 1<<30 {
		t.Fatalf("128bit clamp: cand=%d cur=%d action=%s", r.Cand, r.Cur, r.Action)
	}
	// C=10000：per=ceil(Dt/10000)=1.07e14，仍被 Bmax 夹
	d2, _ := NewDeflator(Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 1024,
		T: 1_000_000, W: 1, ThU: 0, ThD: 0, Kc: 1, C0: 10000,
		Pool: 1 << 40, H: 0})
	r2, _ := d2.Sample(1<<30, 1)
	wantBeff := beff(1<<30, 1<<40, 10000, 1024)
	if r2.Cand != wantBeff {
		t.Fatalf("128bit C=10000 cand=%d want beff=%d", r2.Cand, wantBeff)
	}
}

func TestWindowOpsTwoWays(t *testing.T) {
	// 窗口未满：每样本 1 次操作；已满：每样本 2 次（1 出 + 1 入），与 W 无关。
	for _, w := range []int{3, 100} {
		cfg := exampleCfg()
		cfg.W = w
		cfg.B0 = 1024
		d, _ := NewDeflator(cfg)
		var before int64
		for i := 0; i < w+10; i++ {
			if i == w {
				before = d.State().WindowOps
			}
			d.Sample(2000, 1000) // 低吞吐，cand 恒为 1024 => Hold，无 pol
		}
		after := d.State().WindowOps
		if after-before != 20 { // 已满后每个样本增量恒为 2
			t.Fatalf("W=%d full-window ops delta=%d want 20", w, after-before)
		}
	}
	// 两档在「已满后的同一类样本」上增量相同
	ops := func(w int) int64 {
		cfg := exampleCfg()
		cfg.W = w
		cfg.B0 = 1024
		d, _ := NewDeflator(cfg)
		for i := 0; i < w; i++ {
			d.Sample(2000, 1000)
		}
		base := d.State().WindowOps
		for i := 0; i < 5; i++ {
			d.Sample(2000, 1000)
		}
		return d.State().WindowOps - base
	}
	if ops(3) != ops(100) || ops(3) != 10 {
		t.Fatalf("ops mismatch: %d vs %d", ops(3), ops(100))
	}
}

func TestConcurrentAccess(t *testing.T) {
	d, _ := NewDeflator(exampleCfg())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				switch (seed + i) % 7 {
				case 0:
					d.Sample(30000, 1000)
				case 1:
					d.Sample(0, 1000)
				case 2:
					d.SetChannels(1 + (seed+i)%32)
				case 3:
					d.Pause()
				case 4:
					d.Resume()
				case 5:
					st := d.State()
					_ = st
				default:
					d.Sample(1, 1)
				}
			}
		}(g)
	}
	wg.Wait()
	st := d.State()
	if st.Cur%1024 != 0 {
		t.Fatalf("cur not G-aligned: %d", st.Cur)
	}
	if st.Cur > beff(32768, 65536, st.Channels, 1024) {
		t.Fatalf("invariant cur<=beff: cur=%d ch=%d", st.Cur, st.Channels)
	}
}
