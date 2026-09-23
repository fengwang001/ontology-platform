package durable

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndAdvance(t *testing.T) {
	cases := []struct {
		name    string
		start   uint64
		adv     []uint64
		wantErr error
	}{
		{"first boot from start", 100, []uint64{150, 200}, nil},
		{"zero start", 0, []uint64{1}, nil},
		{"regression rejected", 10, []uint64{20, 5}, ErrRegression},
		{"equal rejected", 10, []uint64{20, 20}, ErrRegression},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "counter")
			c, err := Open(p, tc.start)
			if err != nil {
				t.Fatal(err)
			}
			if c.Value() != tc.start {
				t.Fatalf("initial value = %d, want %d", c.Value(), tc.start)
			}
			var last error
			for _, to := range tc.adv {
				if last = c.Advance(to); last != nil {
					break
				}
			}
			if !errors.Is(last, tc.wantErr) {
				t.Fatalf("advance err = %v, want %v", last, tc.wantErr)
			}
		})
	}
	// 推进后重开，值必须持久。
	p := filepath.Join(t.TempDir(), "counter")
	c, _ := Open(p, 7)
	if err := c.Advance(99); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(p, 7)
	if err != nil || c2.Value() != 99 {
		t.Fatalf("reopen = %d, %v; want 99, nil", c2.Value(), err)
	}
}

// 逐字节截断：每个截断点都必须被分类且拒绝启动，绝不回退到 0。
func TestTruncation(t *testing.T) {
	src := filepath.Join(t.TempDir(), "counter")
	c, err := Open(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(12345); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]error{}
	for n := 1; n < len(full); n++ {
		switch {
		case n < magicLen:
			want[n] = ErrHeaderIncomplete
		case n < magicLen+valueLen:
			want[n] = ErrValueIncomplete
		default:
			want[n] = ErrCRC
		}
	}
	for n := 1; n < len(full); n++ {
		p := filepath.Join(t.TempDir(), "counter")
		if err := os.WriteFile(p, full[:n], 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Open(p, 0)
		if !errors.Is(err, want[n]) {
			t.Fatalf("truncate to %d: err = %v, want %v", n, err, want[n])
		}
		if got != nil {
			t.Fatalf("truncate to %d: started with value %d, must refuse", n, got.Value())
		}
	}
	// 全长但 CRC 损坏同样拒绝。
	p := filepath.Join(t.TempDir(), "counter")
	bad := append([]byte(nil), full...)
	bad[totalLen-1] ^= 0xFF
	if err := os.WriteFile(p, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, 0); !errors.Is(err, ErrCRC) {
		t.Fatalf("corrupt crc: err = %v, want %v", err, ErrCRC)
	}
}

// 写失败：Advance 报错且已持久化的值不被部分覆盖。
func TestWriteFault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "counter")
	c, err := Open(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(50); err != nil {
		t.Fatal(err)
	}
	c.InjectFault(errors.New("disk full"))
	if err := c.Advance(90); !errors.Is(err, ErrWrite) {
		t.Fatalf("advance err = %v, want ErrWrite", err)
	}
	if c.Value() != 50 {
		t.Fatalf("in-memory value = %d, want 50", c.Value())
	}
	c2, err := Open(p, 0)
	if err != nil || c2.Value() != 50 {
		t.Fatalf("on-disk value = %d, %v; want 50, nil", c2.Value(), err)
	}
	c.InjectFault(nil)
	if err := c.Advance(90); err != nil {
		t.Fatal(err)
	}
}
