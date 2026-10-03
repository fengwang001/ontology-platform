package scrape_test

import (
	"errors"
	"math/rand"
	"testing"
)

var errFetch = errors.New("fetch failed")

// 随机操作序列：真实实现与朴素模拟逐步对照, 日志打印输入/输出/判定依据。
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	h, q, s := newEnv(t, 300, 100, 8, 2)
	m := newModel(300, 100, 8, 2)
	targets := []string{"t0", "t1", "t2"}
	names := []string{"a", "b", "c"}
	nows := map[string]int64{}
	realFetch := func(spec fetchSpec) func() (map[string]int64, error) {
		return func() (map[string]int64, error) {
			switch spec.kind {
			case 1:
				return nil, errFetch
			case 2:
				panic("boom")
			}
			return spec.payload, nil
		}
	}
	for i := 0; i < 400; i++ {
		tg := targets[rng.Intn(len(targets))]
		now := nows[tg] + int64(rng.Intn(150))
		if rng.Intn(10) == 0 {
			now = nows[tg] // 触发时钟回退
		}
		if rng.Intn(6) == 0 { // 原始 Append 制造乱序/冲突/过旧
			name := tg + "/" + names[rng.Intn(len(names))]
			ts, v, stale := nows[tg]+int64(rng.Intn(200))-100, int64(rng.Intn(10)), rng.Intn(5) == 0
			var rErr, mErr error
			if stale {
				rErr, mErr = h.AppendStale(name, ts), m.append(name, ts, 0, true)
			} else {
				rErr, mErr = h.Append(name, ts, v), m.append(name, ts, v, false)
			}
			if rErr != mErr {
				t.Fatalf("op%d Append(%q,%d,%d,%v): real=%v model=%v", i, name, ts, v, stale, rErr, mErr)
			}
			t.Logf("op%d 输入 Append(%q,%d,v=%d,stale=%v) 输出 real=%v model=%v 判定一致", i, name, ts, v, stale, rErr, mErr)
		} else {
			spec := fetchSpec{kind: rng.Intn(8), payload: map[string]int64{}}
			if spec.kind > 2 {
				spec.kind = 0
			}
			for _, n := range names {
				if rng.Intn(2) == 0 {
					spec.payload[n] = int64(rng.Intn(100))
				}
			}
			switch rng.Intn(15) {
			case 0:
				spec.payload["x"], spec.payload["y"], spec.payload["z"] = 1, 2, 3 // 超 Lim
			case 1:
				spec.payload["up"] = 1 // 非法名字
			}
			rRes, rErr := s.Scrape(now, tg, realFetch(spec))
			mRes, mErr := m.scrape(now, tg, spec)
			if rErr != mErr || rRes != mRes {
				t.Fatalf("op%d Scrape(%d,%q,%+v): real=(%v,%v) model=(%v,%v)", i, now, tg, spec, rRes, rErr, mRes, mErr)
			}
			t.Logf("op%d 输入 Scrape(%d,%q,kind=%d,payload=%v) 输出 real=(%+v,%v) model=(%+v,%v) 判定一致",
				i, now, tg, spec.kind, spec.payload, rRes, rErr, mRes, mErr)
			if rErr == nil {
				nows[tg] = now
			}
		}
		for k := 0; k < 2; k++ { // 随机瞬时查询对照
			name := targets[rng.Intn(3)] + "/" + []string{"a", "b", "c", "up"}[rng.Intn(4)]
			ts := int64(rng.Intn(int(nows[targets[rng.Intn(3)]] + 400)))
			got, want := q.Instant(name, ts), m.instant(name, ts)
			if got != want {
				t.Fatalf("op%d Instant(%q,%d): real=%v model=%v", i, name, ts, got, want)
			}
			t.Logf("op%d 输入 Instant(%q,%d) 输出 %v 判定一致", i, name, ts, got)
		}
	}
}
