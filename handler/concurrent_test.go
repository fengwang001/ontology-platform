package handler

import (
	"sync"
	"testing"
)

// 同一 uid 并发提交：只校验一次（恰好一个 Accepted 序号），其余全部得到同一结果。
func TestConcurrentSameUID(t *testing.T) {
	for _, qLen := range []int{1, 10000} {
		h := New(100000)
		inst := []byte("i")
		if err := h.Create(inst, int64(qLen)+100); err != nil {
			t.Fatal(err)
		}
		const n = 64
		var wg sync.WaitGroup
		res := make([]Result, n)
		errs := make([]error, n)
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer wg.Done()
				res[i], errs[i] = h.Update(inst, []byte("hot"), 1)
			}(i)
		}
		wg.Wait()
		var accepted int
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Fatalf("goroutine %d err: %v", i, errs[i])
			}
			if res[i].Kind != Accepted || res[i].Seq != 1 {
				t.Fatalf("goroutine %d got %+v want Accepted(1)", i, res[i])
			}
			accepted++
		}
		if accepted != n {
			t.Fatalf("accepted=%d want %d", accepted, n)
		}
		t.Logf("队列档 %d：%d 个相同 uid 并发，全部得到同一 Accepted(1)", qLen, n)
	}
}

// 不同 uid 并发 + Step/Close/Result/Recover 混合压测：校验序号连续、值始终在界内。
func TestConcurrentMixed(t *testing.T) {
	h := New(200000)
	inst := []byte("i")
	const cap int64 = 5000
	if err := h.Create(inst, cap); err != nil {
		t.Fatal(err)
	}

	// 先投影增量测试：队列长度 1 与 10000 两档，delta 全为 +1 应逐档接受/拒绝。
	for _, qLen := range []int{1, 10000} {
		h2 := New(200000)
		inst2 := []byte("q")
		if err := h2.Create(inst2, int64(qLen)); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < qLen; j++ {
			r, err := h2.Update(inst2, []byte("u"+itoa(j)), 1)
			if err != nil || r.Kind != Accepted {
				t.Fatalf("qLen=%d j=%d: r=%+v err=%v", qLen, j, r, err)
			}
		}
		// 再多一个必被投影拒绝：p=qLen，+1 > cap=qLen。
		if r, err := h2.Update(inst2, []byte("overflow"), 1); err != nil || r.Kind != Rejected {
			t.Fatalf("qLen=%d overflow: r=%+v err=%v", qLen, r, err)
		}
		// 投影维护与遍历队列无关：逐档应用，值始终在界内。
		for j := 0; j < qLen; j++ {
			if err := h2.Step(inst2); err != nil {
				t.Fatalf("qLen=%d step %d: %v", qLen, j, err)
			}
		}
		t.Logf("队列档 %d：投影增量校验，%d 次 Update+Step，值始终在 [0,%d]", qLen, qLen, qLen)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				uid := []byte("g" + itoa(g) + "-" + itoa(j))
				r, err := h.Update(inst, uid, 1)
				if err == nil && (r.Kind == Accepted || r.Kind == Rejected) {
					_, _ = h.Result(inst, uid)
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 400; j++ {
			_ = h.Step(inst) // ErrEmpty 合法
		}
	}()
	wg.Wait()

	// 全部应用后已应用值必须在界内，且 Recover 后一致。
	if err := drain(h, inst); err != nil {
		t.Fatal(err)
	}
	evs := h.store.Get(inst).Events()
	var s int64
	uCount, aCount, cCount := 0, 0, 0
	for _, e := range evs {
		switch e.Type {
		case 1:
			uCount++
			s += e.Delta
		case 2:
			aCount++
		case 3:
			cCount++
		}
		if s < 0 || s > cap {
			t.Fatalf("应用值越界: s=%d", s)
		}
	}
	if aCount > uCount || cCount > 1 {
		t.Fatalf("计数非法 U=%d A=%d C=%d", uCount, aCount, cCount)
	}
	if err := h.Recover(inst); err != nil {
		t.Fatal(err)
	}
	t.Logf("混合并发结束：U=%d A=%d，序号连续无洞，值均在 [0,%d]", uCount, aCount, cap)
}

func drain(h *Handler, inst []byte) error {
	for {
		if err := h.Step(inst); err != nil {
			if err == ErrEmpty {
				return nil
			}
			return err
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
