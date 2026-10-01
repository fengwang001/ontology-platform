package vegas

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, c Config) *Limiter {
	t.Helper()
	l, err := New(c)
	if err != nil {
		t.Fatalf("New(%+v): %v", c, err)
	}
	return l
}

func baseCfg() Config {
	return Config{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100}
}

func resName(r Result) string {
	switch r {
	case Success:
		return "success"
	case Dropped:
		return "dropped"
	case Ignored:
		return "ignored"
	default:
		return fmt.Sprintf("invalid(%d)", int(r))
	}
}

// acquire/release 在测试日志中打印输入、输出与判定依据。
func acquire(t *testing.T, l *Limiter, now int64) (Token, error) {
	t.Helper()
	tk, err := l.Acquire(now)
	t.Logf("Acquire(now=%d) -> token={seq=%d w=%d exp=%d} err=%v | L=%d n=%d",
		now, tk.Seq, tk.W, tk.ExpiresAt, err, l.L(), l.N())
	return tk, err
}

func release(t *testing.T, l *Limiter, seq int64, res Result, rtt, now int64) error {
	t.Helper()
	err := l.Release(seq, res, rtt, now)
	t.Logf("Release(seq=%d result=%s rtt=%d now=%d) -> err=%v | L=%d n=%d m=%d",
		seq, resName(res), rtt, now, err, l.L(), l.N(), l.MinRTT())
	return err
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{L0: 10, Lmin: 0, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 5, Lmax: 4, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 1_000_001, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 1, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 13, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: -1, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 4, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 1, Tmo: 50, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 0, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1_000_000_001, Cooldown: 10, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 0},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 10, Wm: 1_000_000_001},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: -1, Wm: 100},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cooldown: 1_000_000_001, Wm: 100},
	}
	for i, c := range bad {
		if _, err := New(c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: want ErrInvalidConfig, got %v", i, err)
		}
	}
	edges := []Config{
		{L0: 1, Lmin: 1, Lmax: 1_000_000, Alpha: 0, Beta: 1, Tmo: 1, Cooldown: 0, Wm: 1},
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1_000_000_000, Cooldown: 1_000_000_000, Wm: 1_000_000_000},
	}
	for i, c := range edges {
		if _, err := New(c); err != nil {
			t.Fatalf("edge case %d should be valid: %v", i, err)
		}
	}
}
