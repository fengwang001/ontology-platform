package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestNewRejectsBadQuantum：配置非法 quantum<=0 返回 ErrBadConfig，且拿不到实例。
func TestNewRejectsBadQuantum(t *testing.T) {
	for _, q := range []int64{0, -1, -100} { // 表驱动多档
		s, err := New(q)
		if !errors.Is(err, ErrBadConfig) || s != nil {
			t.Fatalf("quantum=%d: err=%v s=%v, want ErrBadConfig,nil", q, err, s)
		}
	}
}

// TestAddValidation：进程非法（arrival<0 或 burst<=0）归入 ErrBadProc 一类。
func TestAddValidation(t *testing.T) {
	cases := []struct {
		name        string
		pid, ar, bu int64
		wantBadProc bool
	}{
		{"arrival negative", 1, -1, 1, true},
		{"burst zero", 1, 0, 0, true},
		{"burst negative", 1, 3, -7, true},
		{"ok", 1, 0, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(4)
			if err != nil {
				t.Fatal(err)
			}
			err = s.Add(c.pid, c.ar, c.bu)
			if ErrBadProc(err) != c.wantBadProc {
				t.Fatalf("err=%v wantBadProc=%v", err, c.wantBadProc)
			}
		})
	}
}

// TestAddDuplicate：重复 pid 返回 ErrDupPID，且与配置/字段类互不相同。
func TestAddDuplicate(t *testing.T) {
	s, _ := New(4)
	if err := s.Add(1, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(1, 2, 3); !errors.Is(err, ErrDupPID) {
		t.Fatalf("dup err=%v want ErrDupPID", err)
	}
}

// TestRejectedAddLeavesState 钉住不变量 4：被拒操作不留痕，之后仍可正常使用。
func TestRejectedAddLeavesState(t *testing.T) {
	s, _ := New(4)
	if err := s.Add(9, 0, 4); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func() error{
		func() error { return s.Add(9, 0, 1) },   // 重复 pid
		func() error { return s.Add(10, -1, 1) }, // arrival<0
		func() error { return s.Add(11, 0, 0) },  // burst<=0
	} {
		if err := bad(); err == nil {
			t.Fatal("expected rejection")
		}
	}
	got, err := s.Run() // 集合仍只有 pid 9；burst4=quantum，t=4 完成
	if err != nil || !reflect.DeepEqual(got, map[int64]int64{9: 4}) {
		t.Fatalf("state after rejections: %v err=%v, want map[9:4]", got, err)
	}
	if err := s.Add(12, 4, 2); err != nil { // 拒绝后仍可继续正常使用
		t.Fatalf("add after rejects: %v", err)
	}
	if got, _ := s.Run(); got[12] != 6 { // P9 完@4，P12 到@4 burst2 完@6
		t.Fatalf("pid12 completion %d, want 6", got[12])
	}
}

// TestRunFixed：对外 Run 的第三节四进程结果精确等于手算值。
func TestRunFixed(t *testing.T) {
	s, _ := New(4)
	for _, x := range [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}} {
		if err := s.Add(x[0], x[1], x[2]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Run()
	want := map[int64]int64{1: 19, 2: 8, 3: 11, 4: 13}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v err=%v want %v", got, err, want)
	}
}

// TestConcurrentAdd：N 个 goroutine 各 Add 一个不同 pid，无 sleep；
// 完成时刻数量恰为 N，且与顺序添加的结果完全一致（即每个都正确）。
func TestConcurrentAdd(t *testing.T) {
	const N = 200
	spec := [N][2]int64{}
	for i := range spec { // 循环生成确定参数
		spec[i] = [2]int64{int64(i % 17), int64(1 + (i*7)%13)}
	}
	s, _ := New(3)
	var wg sync.WaitGroup
	for i, p := range spec {
		wg.Add(1)
		go func(pid int64, sp [2]int64) {
			defer wg.Done()
			if err := s.Add(pid, sp[0], sp[1]); err != nil {
				t.Errorf("concurrent add pid=%d: %v", pid, err)
			}
		}(int64(i+1), p)
	}
	wg.Wait()
	got, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != N {
		t.Fatalf("completion count %d != N=%d", len(got), N)
	}
	ref, _ := New(3) // 顺序添加的参照：结果应与并发添加一致
	for i, sp := range spec {
		if err := ref.Add(int64(i+1), sp[0], sp[1]); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := ref.Run()
	if !reflect.DeepEqual(got, want) {
		t.Fatal("concurrent add result differs from sequential reference")
	}
}

func TestSelfCheck(t *testing.T) {
	if s, err := New(4); err != nil {
		t.Fatal(err)
	} else if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
