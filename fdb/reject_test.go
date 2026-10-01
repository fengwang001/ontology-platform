package fdb

import (
	"errors"
	"testing"
)

// TestRejections 验证各类拒绝原因，且拒绝不改变任何表项、时钟与计数。
func TestRejections(t *testing.T) {
	f := mustNew(t, 2, 100, 10)
	if _, err := f.Frame(0, macA, macM, 1, 10); err != nil {
		t.Fatal(err)
	}
	snapshot := f.Stats()

	if _, err := f.Frame(2, macA, macM, 1, 11); !errors.Is(err, ErrPortRange) {
		t.Fatalf("want ErrPortRange, got %v", err)
	}
	if _, err := f.Frame(-1, macA, macM, 1, 11); !errors.Is(err, ErrPortRange) {
		t.Fatalf("want ErrPortRange, got %v", err)
	}
	if _, err := f.Frame(0, macA, macM, 0, 11); !errors.Is(err, ErrVLANRange) {
		t.Fatalf("want ErrVLANRange(vlan0), got %v", err)
	}
	if _, err := f.Frame(0, macA, macM, 4095, 11); !errors.Is(err, ErrVLANRange) {
		t.Fatalf("want ErrVLANRange(vlan4095), got %v", err)
	}
	if _, err := f.Frame(0, macB, macM, 1, 9); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("want ErrClockBackward, got %v", err)
	}
	if err := f.AddStatic(1, macB, 0, 9); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("AddStatic want ErrClockBackward, got %v", err)
	}
	if _, err := f.FlushPort(0, 9); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("FlushPort want ErrClockBackward, got %v", err)
	}
	// 上述全部调用均被拒绝；计数与表项必须保持拒绝前快照。
	if st := f.Stats(); st != snapshot {
		t.Fatalf("rejected calls changed stats: before %+v after %+v", snapshot, st)
	}
	if f.Len() != 1 {
		t.Fatalf("rejected calls changed table, Len=%d", f.Len())
	}
	// 被拒绝后时钟仍是 10：相同时刻调用合法（只拒绝严格更早）。
	if _, err := f.Frame(0, macA, macM, 1, 10); err != nil {
		t.Fatalf("equal timestamp after rejected call must succeed, got %v", err)
	}
	if st := f.Stats(); st.Learned != 1 || st.Floods != 2 {
		t.Fatalf("accepted equal-time frame should process normally: %+v", st)
	}
	t.Logf("拒绝用例: 端口/VLAN/时钟回退均 errors.Is 命中, 表项时钟计数不变")

	if err := f.AddStatic(1, macM, 0, 11); !errors.Is(err, ErrMulticastStatic) {
		t.Fatalf("want ErrMulticastStatic, got %v", err)
	}

	type cfg struct {
		n, c int
		a    int64
	}
	for _, bad := range []cfg{{0, 1, 1}, {65, 1, 1}, {2, 0, 1}, {2, 1, 0}} {
		if _, err := New(bad.n, bad.a, bad.c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %+v want ErrInvalidConfig, got %v", bad, err)
		}
	}
}

// TestNoSharedStorage 验证返回切片彼此独立、不与内部存储共享。
func TestNoSharedStorage(t *testing.T) {
	f := mustNew(t, 3, 100, 10)
	a, _ := f.Frame(0, macA, macB, 1, 0)
	b, _ := f.Frame(0, macC, macB, 1, 1)
	a[0] = 99
	if b[0] != 1 {
		t.Fatalf("returned slices share storage: mutation leaked into b=%v", b)
	}
}
