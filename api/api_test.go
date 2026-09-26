package api_test

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestRejectedEdgesLeaveNoTrace 钉住不变量 4：四类非法操作各报互不相同的
// 哨兵错误，被拒后图状态不变且仍可继续正常使用。
func TestRejectedEdgesLeaveNoTrace(t *testing.T) {
	if _, err := api.New(-1); !errors.Is(err, api.ErrInvalidN) {
		t.Fatalf("New(-1) = %v, 期望 ErrInvalidN", err)
	}
	a, err := api.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		u, v int
		want error
	}{
		{"越界u", -1, 2, api.ErrOutOfRange},
		{"越界v", 1, 4, api.ErrOutOfRange},
		{"自环", 2, 2, api.ErrSelfLoop},
		{"重复边", 1, 0, api.ErrDuplicate},
	}
	_, errN := api.New(-2)
	var got []error
	for _, c := range cases {
		err := a.AddEdge(c.u, c.v)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		got = append(got, err)
	}
	// 四个类别两两互异：n 非法 / 越界 / 自环 / 重复边。
	rep := []error{errN, got[0], got[2], got[3]}
	for i := range rep {
		for j := i + 1; j < len(rep); j++ {
			if rep[i] == rep[j] {
				t.Errorf("哨兵错误不互异: %v", rep[i])
			}
		}
	}
	if a.EdgeCount() != 1 {
		t.Fatalf("被拒后 EdgeCount=%d, 期望 1", a.EdgeCount())
	}
	if err := a.AddEdge(2, 3); err != nil || a.EdgeCount() != 2 {
		t.Fatalf("被拒后无法继续加边: err=%v m=%d", err, a.EdgeCount())
	}
}

// TestConcurrentReaders Compute 完成后并发读取，结果必须逐项相同。
func TestConcurrentReaders(t *testing.T) {
	a, err := api.New(6)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}} {
		if err := a.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	a.Compute()
	wantPEO := a.PEO()
	var bad atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if !a.IsChordal() || a.FirstViolator() != -1 || a.EdgeCount() != 6 ||
					!reflect.DeepEqual(a.PEO(), wantPEO) || a.SelfCheck() != nil {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("并发读取出现 %d 次不一致", bad.Load())
	}
}

func TestSelfCheck(t *testing.T) {
	a, err := api.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
