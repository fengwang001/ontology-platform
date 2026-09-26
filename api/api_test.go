package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/crc"
	"ontology/poly"
)

func TestKnownVector(t *testing.T) {
	c, err := api.New(poly.IEEE)
	if err != nil {
		t.Fatal(err)
	}
	c.Update([]byte("123456789"))
	if got := c.Value(); got != 0xCBF43926 {
		t.Fatalf("Value() = %#08x, want 0xcbf43926", got)
	}
	if err := c.Verify(0xCBF43926); err != nil {
		t.Fatalf("Verify 正确值被拒: %v", err)
	}
}

// 任意切分点，分段 Update 与一次 Update 结果相同（不变量 3）。
func TestConcatProperty(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	for _, n := range []int{1, 2, 3, 17, 100, 999} {
		data := make([]byte, n)
		r.Read(data)
		one, _ := api.New(poly.IEEE)
		one.Update(data)
		for cut := 0; cut <= n; cut++ {
			two, _ := api.New(poly.IEEE)
			two.Update(data[:cut])
			two.Update(data[cut:])
			if two.Value() != one.Value() {
				t.Fatalf("n=%d cut=%d: %#08x != %#08x", n, cut, two.Value(), one.Value())
			}
		}
	}
}

// 三类错误互不相同，且被拒后累计状态不变、仍可正常使用（不变量 4）。
func TestRejectedOpsKeepState(t *testing.T) {
	for _, p := range []uint32{0, 0x04C11DB7, 0x7FFFFFFF} {
		if _, err := api.New(p); !errors.Is(err, api.ErrInvalidPoly) {
			t.Fatalf("poly=%#x: err = %v, want ErrInvalidPoly", p, err)
		}
	}
	if api.ErrInvalidPoly == api.ErrEmptyVerify || api.ErrEmptyVerify == api.ErrMismatch ||
		api.ErrInvalidPoly == api.ErrMismatch {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	c, _ := api.New(poly.IEEE)
	if err := c.Verify(0); !errors.Is(err, api.ErrEmptyVerify) {
		t.Fatalf("空校验: err = %v, want ErrEmptyVerify", err)
	}
	if c.BytesProcessed() != 0 {
		t.Fatal("空校验被拒后字节数改变")
	}
	c.Update([]byte("keep me"))
	v, n := c.Value(), c.BytesProcessed()
	if err := c.Verify(v ^ 0xFF); !errors.Is(err, api.ErrMismatch) {
		t.Fatalf("不匹配: err = %v, want ErrMismatch", err)
	}
	if c.Value() != v || c.BytesProcessed() != n {
		t.Fatal("Verify 被拒后累计状态改变")
	}
	c.Update([]byte("123456789"[0:1])) // 被拒后仍可继续正常使用
	if c.Verify(c.Value()) != nil {
		t.Fatal("被拒后实例不可用")
	}
}

func TestSelfCheck(t *testing.T) {
	c, _ := api.New(poly.IEEE)
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 并发：N 个 goroutine 独立 Update 后取 Value 与逐位参照一致；
// 另 N 个 goroutine 并发只读同一实例，结果完全相同。不用 sleep。
func TestConcurrent(t *testing.T) {
	const N = 16
	shared, _ := api.New(poly.IEEE)
	shared.Update([]byte("shared payload"))
	want := shared.Value()

	var wg sync.WaitGroup
	errs := make(chan string, 3*N)
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func(seed int64) {
			defer wg.Done()
			buf := make([]byte, 512)
			rand.New(rand.NewSource(seed)).Read(buf)
			mine, _ := api.New(poly.IEEE)
			mine.Update(buf)
			ref := crc.New(poly.IEEE)
			ref.UpdateBitwise(buf)
			if mine.Value() != ref.Value() {
				errs <- "独立实例与逐位参照不一致"
			}
		}(int64(i))
		go func() {
			defer wg.Done()
			if shared.Value() != want || shared.BytesProcessed() != len("shared payload") ||
				shared.SelfCheck() != nil {
				errs <- "共享实例并发读不一致"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
