package sla

import "testing"

// 预造 b.N 笔已送达、延误落最高档、处于申请窗口内的订单，计时区只做 Claim。
func prepareClaims(b *testing.B, extraWeather, extraOrders int) (*System, []string, []int64) {
	b.Helper()
	s := newSystem(testConfig())

	// 目标订单先接受（全局接受时钟单调），时刻最低。
	ids := make([]string, b.N)
	ats := make([]int64, b.N)
	for i := 0; i < b.N; i++ {
		id := "tg" + benchID(i)
		base := int64(i * 200)
		if err := s.Accept(id, base); err != nil {
			b.Fatal(err)
		}
		if err := s.Dispatch(id, base+5); err != nil {
			b.Fatal(err)
		}
		if err := s.Ready(id, base+10); err != nil {
			b.Fatal(err)
		}
		if err := s.Pickup(id, base+12); err != nil {
			b.Fatal(err)
		}
		if err := s.Deliver(id, base+130); err != nil {
			b.Fatal(err)
		}
		ids[i], ats[i] = id, base+135
	}

	for i := 0; i < extraOrders; i++ {
		id := "bg" + benchID(i)
		if err := s.Accept(id, int64(9_000_000+i*7)); err != nil {
			b.Fatal(err)
		}
	}
	// 不覆盖任何目标承诺时刻（目标承诺时刻为 base+60，base 是 200 的倍数，且 < 800 万）。
	for i := 0; i < extraWeather; i++ {
		l := int64(8_000_000 + i*3)
		if err := s.AddWeather("bw"+benchID(i), l, l+2, 5); err != nil {
			b.Fatal(err)
		}
	}
	return s, ids, ats
}

// BenchmarkClaimFixed：无背景天气、无背景订单。
func BenchmarkClaimFixed(b *testing.B) {
	s, ids, ats := prepareClaims(b, 0, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Claim(ids[i], ats[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClaimManyWeather：10 万天气事件 + 10 万其他订单背景下逐笔裁决。
// 每笔裁决时间应与 BenchmarkClaimFixed 同阶（差值为常数级锁/map 开销），
// 即不随天气事件总数、其他订单数量增长。
func BenchmarkClaimManyWeather(b *testing.B) {
	s, ids, ats := prepareClaims(b, 100000, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Claim(ids[i], ats[i]); err != nil {
			b.Fatal(err)
		}
	}
}

func benchID(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}
