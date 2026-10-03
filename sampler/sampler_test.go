package sampler_test

import (
	"errors"
	"testing"

	"ontology/sampler"
)

func cfg() sampler.Config {
	return sampler.Config{K: 2, Cm: 1, Bm: 10, E: 2, P: 100}
}

func TestFilterOrderAndC(t *testing.T) {
	s, err := sampler.New(cfg())
	if err != nil {
		t.Fatal(err)
	}
	// 不安全、体过大、暂停（暂停初值 0，无请求命中）不推进 c。
	if r, sel := s.Filter("POST", 0, 1, 0); sel || r != sampler.Unsafe {
		t.Fatalf("不安全: %+v %v", r, sel)
	}
	if r, _ := s.Filter("GET", 11, 2, 0); r != sampler.BodyTooLarge {
		t.Fatalf("体过大: %v", r)
	}
	if r, _ := s.Filter("GET", 0, 3, 0); r != sampler.NotSelected || s.C() != 1 {
		t.Fatalf("c=1 应未采中, c=%d", s.C())
	}
	// 时间检查通过后才进入 Filter。
	if err := s.CheckClock(4); err != nil {
		t.Fatal(err)
	}
	if _, sel := s.Filter("GET", 0, 4, 0); !sel {
		t.Fatal("c=2 应采中并分派")
	}
	// c=3 未采中（在途 1 不影响判定）。
	if r, _ := s.Filter("GET", 0, 5, 1); r != sampler.NotSelected {
		t.Fatalf("c=3 应未采中: %v", r)
	}
	// c=4 采中但在途 1≥Cm=1：繁忙，c 已推进，不补发。
	if r, sel := s.Filter("GET", 0, 6, 1); sel || r != sampler.Busy {
		t.Fatalf("繁忙: %+v %v", r, sel)
	}
	if s.C() != 4 {
		t.Fatalf("繁忙应推进 c，得 c=%d", s.C())
	}
}

func TestFailPauseAndSuccessReset(t *testing.T) {
	s, _ := sampler.New(cfg())
	if err := s.CheckClock(10); err != nil {
		t.Fatal(err)
	}
	s.Fail(10) // s=1
	if s.PausedUntil() != 0 {
		t.Fatal("单次失败不应暂停")
	}
	s.Fail(20) // s=2 → pausedUntil=120, s=0
	if s.PausedUntil() != 120 {
		t.Fatalf("pausedUntil=%d 想要 120", s.PausedUntil())
	}
	// 暂停期间失败不计数（不会再次刷新暂停）。
	s.Fail(119)
	if s.PausedUntil() != 120 {
		t.Fatal("暂停期间失败不得刷新 pausedUntil")
	}
	// 恢复边界 now==120 的成功清零（本就是 0），单次失败不再立即暂停。
	s.Success(120)
	s.Fail(121)
	if s.PausedUntil() != 120 {
		t.Fatal("恢复后单次失败 s=1 不应触发新暂停")
	}
}

func TestClockMonotonicAndConfig(t *testing.T) {
	s, _ := sampler.New(cfg())
	if err := s.CheckClock(5); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.CheckClock(4), sampler.ErrClockBack) {
		t.Fatal("回退应报 ErrClockBack 且不改状态")
	}
	if !errors.Is(s.CheckClock(-1), sampler.ErrInvalidTime) {
		t.Fatal("负时间非法")
	}
	if !errors.Is(s.CheckClock(1_000_000_000_000_001), sampler.ErrInvalidTime) {
		t.Fatal("超界时间非法")
	}
	if err := s.CheckClock(5); err != nil { // 拒绝不推进时钟，5 仍合法
		t.Fatalf("相等时间应合法, 得 %v", err)
	}
	good := cfg()
	bounds := []func(*sampler.Config){
		func(c *sampler.Config) { c.K = 0 },
		func(c *sampler.Config) { c.K = 1_000_001 },
		func(c *sampler.Config) { c.Cm = 0 },
		func(c *sampler.Config) { c.Cm = 1_000_001 },
		func(c *sampler.Config) { c.Bm = -1 },
		func(c *sampler.Config) { c.Bm = 1_000_000_001 },
		func(c *sampler.Config) { c.E = 0 },
		func(c *sampler.Config) { c.P = 0 },
		func(c *sampler.Config) { c.P = 1_000_000_001 },
	}
	for _, mut := range bounds {
		bad := good
		mut(&bad)
		if _, err := sampler.New(bad); !errors.Is(err, sampler.ErrInvalidConfig) {
			t.Fatalf("越界配置 %+v 应 ErrInvalidConfig, 得 %v", bad, err)
		}
	}
}
