package tou_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/tou"
)

// TestConcurrent 混发写操作与读账单，配合 -race 验证并发安全；
// 串行等价性由单一 RWMutex 保证。
func TestConcurrent(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	const writers, readers, n = 8, 8, 300
	var rw sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < readers; r++ {
		rw.Add(1)
		go func() {
			defer rw.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = e.Bill("C", at(2024, 3, 1, 0, 0, 0))
				}
			}
		}()
	}
	var ww sync.WaitGroup
	for w := 0; w < writers; w++ {
		ww.Add(1)
		go func(w int) {
			defer ww.Done()
			for i := 0; i < n; i++ {
				tm := at(2024, 1, 1, 0, 0, 0).Add(time.Duration(w*n+i) * time.Minute)
				if tm.Year() > 2024 {
					return
				}
				_ = e.RegisterReading(fmt.Sprintf("C%d", w), tm, int64(i))
			}
		}(w)
	}
	ww.Wait()
	close(stop)
	rw.Wait()
}

// BenchmarkRegisterReading 证明登记读数为摊还 O(1)：
// 仅追加切片与累加贡献，不扫描历史读数。
func BenchmarkRegisterReading(b *testing.B) {
	e := tou.New(testLoc)
	if err := e.RegisterTariff(flatVersion(at(2020, 1, 1, 0, 0, 0), 100)); err != nil {
		b.Fatal(err)
	}
	const pt = "X"
	if err := e.RegisterReading(pt, at(2020, 1, 1, 0, 0, 0), 0); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 每条读数跨日界/月界，切片成本只取决于区间长度而非历史长度。
		tm := at(2020, 1, 1, 0, 0, 1).AddDate(0, 0, i)
		if err := e.RegisterReading(pt, tm, int64(i+1)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBill 证明账单查询不随已封账月数量增长：
// 封账月份保存冻结快照，未封账当月只聚当月桶。
func BenchmarkBill(b *testing.B) {
	for _, closedMonths := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("closedMonths=%d", closedMonths), func(b *testing.B) {
			e := tou.New(testLoc)
			must(b, e.RegisterTariff(flatVersion(at(2020, 1, 1, 0, 0, 0), 100)))
			const pt = "Y"
			cursor := at(2020, 1, 1, 0, 0, 0)
			must(b, e.RegisterReading(pt, cursor, 0))
			for mo := 0; mo < closedMonths; mo++ {
				next := cursor.AddDate(0, 1, 0)
				must(b, e.RegisterReading(pt, next, int64(mo+1)))
				must(b, e.CloseMonth(pt, cursor))
				cursor = next
			}
			probe := cursor.AddDate(0, 1, 0)
			must(b, e.RegisterReading(pt, probe, int64(closedMonths+1)))
			month := cursor
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.Bill(pt, month); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
