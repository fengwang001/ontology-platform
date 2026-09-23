package durable

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenAndNext(t *testing.T) {
	tests := []struct {
		name  string
		start uint64
		n     uint64
		want  uint64 // Next 返回值
		after uint64 // 推进后持久值
	}{
		{"从零起步", 0, 10, 0, 10},
		{"从配置起点", 100, 50, 100, 150},
		{"段长为一", 7, 1, 7, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			c, err := Open(p, tc.start)
			if err != nil {
				t.Fatal(err)
			}
			if c.Value() != tc.start {
				t.Fatalf("初始值=%d, want %d", c.Value(), tc.start)
			}
			got, err := c.Next(tc.n)
			if err != nil || got != tc.want {
				t.Fatalf("Next(%d)=(%d,%v), want (%d,nil)", tc.n, got, err, tc.want)
			}
			re, err := Open(p, 999) // 重开：忽略 start，用持久值
			if err != nil {
				t.Fatal(err)
			}
			if re.Value() != tc.after {
				t.Fatalf("重开后值=%d, want %d", re.Value(), tc.after)
			}
			if fi, _ := os.Stat(p); fi.Size() != fileLen {
				t.Fatalf("文件长度=%d, want %d", fi.Size(), fileLen)
			}
		})
	}
}

func TestTruncationRefusesStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "counter")
	c, err := Open(p, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Next(8); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	classify := func(cut int) error {
		switch {
		case cut < headLen:
			return ErrHeaderIncomplete
		case cut < headLen+valLen:
			return ErrValueIncomplete
		default:
			return ErrCRCMismatch
		}
	}
	for cut := 1; cut < len(full); cut++ {
		bad := filepath.Join(t.TempDir(), "counter")
		if err := os.WriteFile(bad, full[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		nc, err := Open(bad, 0)
		if err == nil {
			t.Fatalf("截断到 %d 字节：居然启动成功（值=%d），绝不允许回退到 0", cut, nc.Value())
		}
		if !errors.Is(err, classify(cut)) {
			t.Fatalf("截断到 %d 字节：错误=%v, want %v", cut, err, classify(cut))
		}
	}
}

func TestCorruptionAndOverflow(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"CRC损坏拒绝启动", func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			if _, err := Open(p, 5); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(p)
			data[headLen] ^= 0xFF // 翻转值区域一个字节
			os.WriteFile(p, data, 0o644)
			if _, err := Open(p, 0); !errors.Is(err, ErrCRCMismatch) {
				t.Fatalf("err=%v, want ErrCRCMismatch", err)
			}
		}},
		{"接近上界拒绝回绕", func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			c, err := Open(p, math.MaxUint64-2)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Next(5); !errors.Is(err, ErrOverflow) {
				t.Fatalf("err=%v, want ErrOverflow", err)
			}
			if _, err := c.Next(2); err != nil {
				t.Fatal(err)
			}
			if c.Value() != math.MaxUint64 {
				t.Fatalf("值=%d, want MaxUint64", c.Value())
			}
			if _, err := c.Next(1); !errors.Is(err, ErrOverflow) {
				t.Fatalf("err=%v, want ErrOverflow", err)
			}
		}},
		{"写失败状态不变", func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			c, err := Open(p, 10)
			if err != nil {
				t.Fatal(err)
			}
			boom := errors.New("注入写失败")
			c.writeFile = func(string, []byte) error { return boom }
			if _, err := c.Next(4); !errors.Is(err, boom) {
				t.Fatalf("err=%v, want 注入错误", err)
			}
			if c.Value() != 10 {
				t.Fatalf("写失败后内存值=%d, want 10", c.Value())
			}
			re, err := Open(p, 0)
			if err != nil || re.Value() != 10 {
				t.Fatalf("写失败后磁盘值=%d,%v, want 10,nil", re.Value(), err)
			}
		}},
		{"改名失败状态不变", func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			c, err := Open(p, 10)
			if err != nil {
				t.Fatal(err)
			}
			boom := errors.New("注入改名失败")
			c.rename = func(string, string) error { return boom }
			if _, err := c.Next(4); !errors.Is(err, boom) {
				t.Fatalf("err=%v, want 注入错误", err)
			}
			if c.Value() != 10 {
				t.Fatalf("改名失败后内存值=%d, want 10", c.Value())
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestConcurrentNextNoOverlap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "counter")
	c, err := Open(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	const g, n = 8, 10
	starts := make([]uint64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := c.Next(n)
			if err != nil {
				t.Error(err)
				return
			}
			starts[i] = s
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, s := range starts {
		for v := s; v < s+n; v++ {
			if seen[v] {
				t.Fatalf("号段重叠于 %d", v)
			}
			seen[v] = true
		}
	}
	if len(seen) != g*n || c.Value() != g*n {
		t.Fatalf("覆盖数=%d 终值=%d, want 均为 %d", len(seen), c.Value(), g*n)
	}
}
