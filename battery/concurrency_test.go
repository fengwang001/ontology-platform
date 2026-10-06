package battery_test

import (
	"runtime"
	"sync"
	"testing"

	"ontology/battery"
)

func TestConcurrentSubmitAndQuery(t *testing.T) {
	m, _ := battery.New(testConfig())

	var writerWG, readerWG sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		writerWG.Add(1)
		go func(id int) {
			defer writerWG.Done()
			base := int64(id * 1_000_000)
			for i := int64(1); i <= 300; i++ {
				_, _ = m.Submit(baseSample(base+i, 0))
			}
		}(w)
	}

	for r := 0; r < 4; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for i := 0; i < 50000; i++ {
				select {
				case <-stop:
					return
				default:
					runtime.Gosched()
					s := m.CurrentSnapshot()
					if s.Latched && (s.AllowedChargeMA != 0 || s.AllowedDischargeMA != 0) {
						t.Errorf("inconsistent snapshot under latch: %+v", s)
						return
					}
				}
			}
		}()
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()
}

func BenchmarkSubmit(b *testing.B) {
	mk := func(n int) *battery.Manager {
		m, _ := battery.New(testConfig())
		for i := int64(1); i <= int64(n); i++ {
			m.Submit(baseSample(i, 0))
		}
		return m
	}
	for _, n := range []int{1_000, 200_000} {
		b.Run("after-"+itoa(n), func(b *testing.B) {
			m := mk(n)
			t := int64(n) + 1
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.Submit(baseSample(t, 0)); err == nil {
					t++
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

var _ = battery.FaultOvercurrent
