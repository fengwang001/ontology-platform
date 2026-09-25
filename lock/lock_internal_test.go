package lock

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

// TestAcquireCheckCountConstant 证明系统天花板由增量维护得到：
// 先让 m 个资源被同一任务持有，再做一次 Acquire 判定，
// 非导出计数器 checked（本次检查过的已持有资源个数）必须恒为 0、不随 m 增长。
func TestAcquireCheckCountConstant(t *testing.T) {
	cases := []struct {
		m       int
		probeP  int
		granted bool
	}{
		{100, 1, false}, {1000, 1, false}, {5000, 1, false}, {10000, 1, false},
		{100, 9, true}, {1000, 9, true}, {10000, 9, true},
	}
	for _, c := range cases {
		mg := NewManager()
		mg.AddTask("low", 1)
		mg.AddTask("probe", c.probeP)
		for i := 0; i < c.m; i++ {
			r := "r" + strconv.Itoa(i)
			mg.AddResource(r)
			if err := mg.Use("low", r); err != nil {
				t.Fatalf("m=%d use: %v", c.m, err)
			}
			if g, err := mg.Acquire("low", r); err != nil || !g {
				t.Fatalf("m=%d setup acquire: granted=%v err=%v", c.m, g, err)
			}
		}
		mg.AddResource("probe-res")
		if err := mg.Use("probe", "probe-res"); err != nil {
			t.Fatalf("m=%d probe use: %v", c.m, err)
		}
		g, err := mg.Acquire("probe", "probe-res")
		if err != nil || g != c.granted {
			t.Fatalf("m=%d probe: granted=%v want %v err=%v", c.m, g, c.granted, err)
		}
		// 检查个数必须恒为 0：判定只读增量值，从未扫描任何已持有资源。
		if mg.checked != 0 {
			t.Fatalf("m=%d checked=%d, want 0 (incremental system ceiling)", c.m, mg.checked)
		}
	}
}

// TestCheckedStaysZeroAfterReject 成功授予与被拒 Acquire 都不得扫描已持有资源。
func TestCheckedStaysZeroAfterReject(t *testing.T) {
	mg := NewManager()
	mg.AddTask("t", 2)
	mg.AddResource("r")
	if err := mg.Use("t", "r"); err != nil {
		t.Fatal(err)
	}
	if g, err := mg.Acquire("t", "r"); err != nil || !g {
		t.Fatalf("setup acquire g=%v err=%v", g, err)
	}
	if mg.checked != 0 {
		t.Fatalf("checked=%d after grant", mg.checked)
	}
	if _, err := mg.Acquire("t", "r"); err != ErrAlreadyHeld {
		t.Fatalf("re-acquire err=%v want ErrAlreadyHeld", err)
	}
	if mg.checked != 0 {
		t.Fatalf("checked=%d after rejected re-acquire", mg.checked)
	}
}

// TestConcurrentMutex：N 个 goroutine 各持互不冲突资源反复 Acquire/Release，无 sleep；
// 授予后立即重入必为 ErrAlreadyHeld（互斥不变量持续成立），天花板始终有界，结束后归零。
func TestConcurrentMutex(t *testing.T) {
	mg := NewManager()
	const N, iters = 16, 300
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		tk, x := "t"+strconv.Itoa(g), "r"+strconv.Itoa(g)
		mg.AddTask(tk, g+1)
		mg.AddResource(x)
		if e := mg.Use(tk, x); e != nil {
			t.Fatal(e)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				for { // 被他人天花板挡住就自旋重试，不用 sleep 制造时序
					ok, e := mg.Acquire(tk, x)
					if e != nil {
						t.Error(e)
						return
					}
					if ok {
						break
					}
				}
				if s := mg.SystemCeiling(); s < 0 || s > N {
					t.Errorf("sys=%d out of range", s)
					return
				}
				if _, e := mg.Acquire(tk, x); !errors.Is(e, ErrAlreadyHeld) {
					t.Errorf("mutex violated on %s: %v", x, e)
					return
				}
				if e := mg.Release(tk, x); e != nil {
					t.Error(e)
					return
				}
			}
		}()
	}
	wg.Wait()
	if mg.SystemCeiling() != 0 {
		t.Fatalf("final sys=%d, want 0", mg.SystemCeiling())
	}
}
