package creditcard

import "testing"

// BenchmarkBillAfterLongHistory shows that billing cost stays flat no
// matter how many periods and transactions preceded it.
func BenchmarkBillAfterLongHistory(b *testing.B) {
	p := zeroRates(0, 1000, 0, 10)
	p.RateBps = [NumCategories]int64{999, 888, 777}
	s := NewService()
	if err := s.CreateAccount("x", p, 0); err != nil {
		b.Fatal(err)
	}
	day := int64(0)
	for i := 0; i < 10000; i++ { // 10k historical periods, 30k transactions
		day++
		_ = s.Charge("x", Cash, 1000, day)
		_ = s.Charge("x", Purchase, 500, day)
		_, _ = s.Pay("x", 100, day)
		_, _ = s.Bill("x", day)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		day++
		if _, err := s.Bill("x", day); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPay(b *testing.B) {
	p := zeroRates(0, 1000, 0, 10)
	p.RateBps = [NumCategories]int64{999, 888, 777}
	s := NewService()
	if err := s.CreateAccount("x", p, 0); err != nil {
		b.Fatal(err)
	}
	_ = s.Charge("x", Cash, 1<<60, 1)
	_ = s.Charge("x", Purchase, 1<<60, 1)
	if _, err := s.Bill("x", 2); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Pay("x", 1, int64(3+i)); err != nil {
			b.Fatal(err)
		}
	}
}
