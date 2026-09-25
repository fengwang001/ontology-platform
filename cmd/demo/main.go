package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/gbn"
	"ontology/snd"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	// ---- snd：第三节十步序列逐步快照 ----
	w, _ := snd.New(4)
	type snap struct {
		base, next int64
		unacked    []int64
	}
	var snaps []snap
	var tmo []int64
	shoot := func(op func()) { op(); snaps = append(snaps, snap{w.Base(), w.Next(), w.Unacked()}) }
	for range 4 {
		shoot(func() { _, _ = w.Send() })
	}
	shoot(func() { w.Advance(2) })      // 5
	shoot(func() { _, _ = w.Send() })   // 6
	shoot(func() { _, _ = w.Send() })   // 7
	shoot(func() { w.Advance(1) })      // 8
	shoot(func() { w.Advance(5) })      // 9
	shoot(func() { tmo = w.Timeout() }) // 10
	want := []snap{
		{0, 1, []int64{0}}, {0, 2, []int64{0, 1}}, {0, 3, []int64{0, 1, 2}},
		{0, 4, []int64{0, 1, 2, 3}}, {2, 4, []int64{2, 3}}, {2, 5, []int64{2, 3, 4}},
		{2, 6, []int64{2, 3, 4, 5}}, {2, 6, []int64{2, 3, 4, 5}}, {5, 6, []int64{5}}, {5, 6, []int64{5}},
	}
	ok10 := len(snaps) == 10
	for i := range want {
		s := snaps[i]
		ok10 = ok10 && s.base == want[i].base && s.next == want[i].next && eq(s.unacked, want[i].unacked)
	}
	check("十步 base/next/未确认 全对拍", ok10)
	check("第5步释放{0,1} 第8步幂等 第10步重传{5}", snaps[4].base == 2 && eq(snaps[4].unacked, []int64{2, 3}) && snaps[7].base == 2 && eq(tmo, []int64{5}))

	// ---- gbn：拒绝、幂等、不留痕 ----
	s, err := gbn.New(4)
	okGBN := err == nil
	for range 4 {
		_, e := s.Send()
		okGBN = okGBN && e == nil
	}
	_, eFull := s.Send() // 窗口已满
	okGBN = okGBN && errors.Is(eFull, gbn.ErrWindowFull) && s.Base() == 0 && s.Next() == 4 && len(s.Unacked()) == 4
	eNeg := s.Ack(-1)
	okGBN = okGBN && errors.Is(eNeg, gbn.ErrBadAck) && s.Base() == 0 && s.Next() == 4
	_, eBadW := gbn.New(0)
	okGBN = okGBN && errors.Is(eBadW, gbn.ErrBadWindow)
	okGBN = okGBN && gbn.ErrBadWindow != gbn.ErrBadAck && gbn.ErrBadAck != gbn.ErrWindowFull && gbn.ErrBadWindow != gbn.ErrWindowFull
	_ = s.Ack(2)
	preB, preN, preU := s.Base(), s.Next(), eq(s.Unacked(), []int64{2, 3})
	_ = s.Ack(2) // 重复
	_ = s.Ack(0) // 乱序
	post := s.Base() == preB && s.Next() == preN && preU && eq(s.Unacked(), []int64{2, 3})
	seq5, eSlide := s.Send() // 滑窗腾出空间后可继续发送
	okGBN = okGBN && post && eSlide == nil && seq5 == 4 && s.Next() == 5
	check("gbn 满窗/负ACK/非法W 三类错误可判定且互不相同", okGBN)
	check("gbn 重复乱序ACK幂等 被拒不留痕 拒绝后仍可用", post)

	// ---- api：SelfCheck、大 m 推进不扫描、并发只读一致 ----
	a, err := api.New(4)
	okAPI := err == nil && a.SelfCheck()
	okAPI = okAPI && snd.SelfCheckAdvanceConstant()
	_, e1 := api.New(-3)
	e2 := a.Ack(-7)
	for range 4 {
		_, _ = a.Send()
	}
	_, e3 := a.Send()
	b0, n0, u0 := a.Base(), a.Next(), eq(a.Unacked(), []int64{0, 1, 2, 3})
	okAPI = okAPI && errors.Is(e1, api.ErrBadWindow) && errors.Is(e2, api.ErrBadAck) && errors.Is(e3, api.ErrWindowFull)
	okAPI = okAPI && b0 == 0 && n0 == 4 && u0 // 被拒后状态不变
	const N = 24
	var wg sync.WaitGroup
	same := true
	type q struct {
		b, n int64
		u    []int64
	}
	samples := make([]q, N)
	wg.Add(N)
	for i := range N {
		go func(i int) { defer wg.Done(); samples[i] = q{a.Base(), a.Next(), a.Unacked()} }(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		same = same && samples[i].b == samples[0].b && samples[i].n == samples[0].n && eq(samples[i].u, samples[0].u)
	}
	check("api 三类哨兵错误可判定且被拒不留痕", okAPI)
	check("api SelfCheck 四条不变量全过", a.SelfCheck())
	check("大m推进检查数不随m增长(恒0)", snd.SelfCheckAdvanceConstant())
	check("并发只读 Base/Next/Unacked 逐字段相同", same)

	if failed {
		fmt.Println("DEMO FAIL")
	}
}
