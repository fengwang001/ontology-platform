package sampler

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
)

// 等权场景下，无论并发提交如何交错，最终样本的键值集合必定等于随机序列中
// 最大的 k 个值——这提供了并发下确定性的、可与批量参照对齐的校验手段。
func TestConcurrentSubmissionsDeterministicKeys(t *testing.T) {
	const n = 200
	const k = 13
	vals := make([]float64, n)
	for i := range vals {
		// 确定性且两两不同的伪随机数（线性同余）。
		vals[i] = float64((int64(i+1)*1103515245+12345)%2147483648) / 2147483648
		if vals[i] <= 0 || vals[i] >= 1 {
			vals[i] = 0.5
		}
	}

	s, seq := newTestSampler(t, k, vals)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := s.Submit(fmt.Sprintf("e-%03d", i), 1); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Submit: %v", err)
	}

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := s.Consumed(); got != n {
		t.Fatalf("consumed=%d want %d", got, n)
	}
	if seq.Consumed() != n || seq.Remaining() != 0 {
		t.Fatalf("random conservation broken: source consumed=%d remaining=%d", seq.Consumed(), seq.Remaining())
	}

	gotKeys := make([]float64, 0, k)
	seenIDs := map[string]bool{}
	for _, sel := range s.Samples() {
		if seenIDs[sel.Element.ID] {
			t.Fatalf("duplicate id %q in sample", sel.Element.ID)
		}
		seenIDs[sel.Element.ID] = true
		gotKeys = append(gotKeys, sel.Key)
	}
	sort.Float64s(gotKeys)

	wantKeys := append([]float64(nil), vals...)
	sort.Float64s(wantKeys)
	wantKeys = wantKeys[n-k:]
	for i := range wantKeys {
		if math.Abs(gotKeys[i]-wantKeys[i]) > 1e-12 {
			t.Fatalf("sample keys %v do not match top-k randoms %v", gotKeys, wantKeys)
		}
	}
}

// 重复标识与非法输入与正常提交并发时，随机性守恒与不留痕不变量仍成立。
func TestConcurrentRejectionsConserveState(t *testing.T) {
	const good = 100
	vals := make([]float64, good)
	for i := range vals {
		vals[i] = float64(int64(i*7+3)%97)/98 + 0.005
	}
	s, seq := newTestSampler(t, 10, vals)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < good; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _ = s.Submit(fmt.Sprintf("good-%03d", i), float64(1+i%5))
		}(i)
	}
	// 一批必然被拒的提交（重复标识、空标识、零/负权重）。
	for j := 0; j < 40; j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			<-start
			switch j % 4 {
			case 0:
				_, _ = s.Submit(fmt.Sprintf("good-%03d", j%good), 1)
			case 1:
				_, _ = s.Submit("", 1)
			case 2:
				_, _ = s.Submit(fmt.Sprintf("zero-%d", j), 0)
			case 3:
				_, _ = s.Submit(fmt.Sprintf("neg-%d", j), -1)
			}
		}(j)
	}
	close(start)
	wg.Wait()

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if s.Consumed() != good || seq.Consumed() != good {
		t.Fatalf("rejections consumed randoms: sampler=%d source=%d", s.Consumed(), seq.Consumed())
	}
	if len(s.Samples()) != 10 {
		t.Fatalf("sample size=%d want 10", len(s.Samples()))
	}
}

// 读接口与提交并发调用必须安全（配合 -race 检测）。
func TestConcurrentReaders(t *testing.T) {
	const n = 300
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 0.1 + 0.8*float64((i*37+11)%100)/100
	}
	s, _ := newTestSampler(t, 7, vals)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < n; i++ {
			_, _ = s.Submit(fmt.Sprintf("r-%03d", i), 1+float64(i%3))
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < n; i++ {
				_ = s.Samples()
				_ = s.Consumed()
				_ = s.Size()
				if err := s.Check(); err != nil {
					t.Errorf("concurrent Check: %v", err)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if err := s.Check(); err != nil {
		t.Fatalf("final Check: %v", err)
	}
}
