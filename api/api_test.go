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

func mustNew(t *testing.T, p uint32) *api.Checker {
	t.Helper()
	c, err := api.New(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// 不变量 2：已知测试向量。
func TestKnownVector(t *testing.T) {
	c := mustNew(t, poly.IEEE)
	c.Update([]byte("123456789"))
	if got := c.Value(); got != 0xCBF43926 {
		t.Fatalf("Value()=%08X want CBF43926", got)
	}
	if err := c.Verify(0xCBF43926); err != nil {
		t.Fatal(err)
	}
}

// 不变量 3：任意切分点拼接结果相同。
func TestConcatenation(t *testing.T) {
	data := []byte("ontology-platform-crc32-concatenation")
	whole := mustNew(t, poly.IEEE)
	whole.Update(data)
	for _, cut := range []int{0, 1, 7, len(data) / 2, len(data) - 1, len(data)} {
		c := mustNew(t, poly.IEEE)
		c.Update(data[:cut])
		c.Update(data[cut:])
		if c.Value() != whole.Value() {
			t.Fatalf("cut=%d: %08X != %08X", cut, c.Value(), whole.Value())
		}
	}
}

// 不变量 4：三类拒绝互不相同、不改状态、拒后仍可用。
func TestFailureLeavesNoTrace(t *testing.T) {
	for _, p := range []uint32{0, 0x04C11DB7, 0x7FFFFFFF} {
		if _, err := api.New(p); !errors.Is(err, api.ErrInvalidPoly) {
			t.Fatalf("poly=%08X: err=%v want ErrInvalidPoly", p, err)
		}
	}
	c := mustNew(t, poly.IEEE)
	if err := c.Verify(0xDEADBEEF); !errors.Is(err, api.ErrEmptyVerify) {
		t.Fatalf("空校验: err=%v want ErrEmptyVerify", err)
	}
	if c.BytesProcessed() != 0 {
		t.Fatal("空校验后字节数被改变")
	}
	c.Update([]byte("payload"))
	want := c.Value()
	if err := c.Verify(want ^ 1); !errors.Is(err, api.ErrMismatch) {
		t.Fatalf("不匹配: err=%v want ErrMismatch", err)
	}
	if c.Value() != want || c.BytesProcessed() != 7 {
		t.Fatal("被拒后累计状态被改变")
	}
	c.Update([]byte("!")) // 拒绝后仍可继续正常使用
	fresh := mustNew(t, poly.IEEE)
	fresh.Update([]byte("payload!"))
	if c.Value() != fresh.Value() {
		t.Fatal("被拒后实例不可继续使用")
	}
}

// 三个哨兵错误两两不可互相匹配。
func TestErrorsDistinct(t *testing.T) {
	es := []error{api.ErrInvalidPoly, api.ErrEmptyVerify, api.ErrMismatch}
	for i, a := range es {
		for j, b := range es {
			if i != j && errors.Is(a, b) {
				t.Fatalf("错误 %v 与 %v 不可区分", a, b)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 并发：N 个写者各自对随机数据 Update 后与逐位参照逐字节相同；
// N 个读者并发读同一已喂满实例，结果完全相同；并发调用 SelfCheck。
// 不用 sleep，靠 WaitGroup 同步。
func TestConcurrent(t *testing.T) {
	const n = 16
	tab := crc.NewTable(poly.IEEE)
	shared := mustNew(t, poly.IEEE)
	shared.Update([]byte("shared-instance"))
	want := shared.Value()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(3)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			buf := make([]byte, 1+r.Intn(2048))
			r.Read(buf)
			c := mustNew(t, poly.IEEE)
			c.Update(buf)
			d := crc.NewDigest(tab, 0xFFFFFFFF)
			d.UpdateBitwise(buf)
			if c.Value() != d.Raw()^0xFFFFFFFF {
				t.Errorf("写者 %d: %08X != %08X", seed, c.Value(), d.Raw()^0xFFFFFFFF)
			}
		}(int64(g))
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if shared.Value() != want || shared.BytesProcessed() != 15 {
					t.Errorf("读者读到不一致的值")
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			if err := api.SelfCheck(); err != nil {
				t.Errorf("并发 SelfCheck: %v", err)
			}
		}()
	}
	wg.Wait()
}
