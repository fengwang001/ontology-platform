package speq

import (
	"sync"
	"testing"
)

// TestConcurrentSafety 在 -race 下验证并发调用安全；
// 所有 goroutine 共用同一单调日期（无回退），任何错误只可能来自合法的
// 业务拒绝（如已报废、状态不允许），不检查具体返回。
func TestConcurrentSafety(t *testing.T) {
	s := New()
	must(t, s.AddCategory(CategoryConfig{
		Code: "B", Kind: KindDevice, PeriodMonths: 120,
		EarlyWindowDays: 30, MinUnsealDays: 1, WarningLeadDays: 50,
	}))
	must(t, s.AddCategory(CategoryConfig{
		Code: "SV", Kind: KindSafetyValve, PeriodMonths: 120,
		EarlyWindowDays: 30, MinUnsealDays: 1, WarningLeadDays: 50,
	}))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				date := 100000 + g*1000 + i
				did := "D" + itoa(g) + "-" + itoa(i)
				vid := "V" + itoa(g) + "-" + itoa(i)
				if _, err := s.RegisterDevice(date, did, "B", date); err == nil {
					_, _ = s.RegisterAttachment(date, vid, "SV", KindSafetyValve, date)
					_ = s.MountAttachment(date+1, vid, did)
					_, _ = s.CheckUsable(date+2, did)
					_, _ = s.QueryWarnings(date + 2)
					_ = s.RegisterUse(date+2, did)
				}
			}
		}(g)
	}
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [4]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
