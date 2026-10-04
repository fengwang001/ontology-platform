package route_test

import (
	"sync"
	"testing"

	"ontology/route"
)

func TestConcurrentEquivalentToSerial(t *testing.T) {
	r := route.New(0, 10)
	if err := r.AssignBlock("1380", 11, 1, 0); err != nil {
		t.Fatal(err)
	}
	const workers = 16
	var wg sync.WaitGroup
	// Monotone per-worker time slices prevent intended clock-rewind rejections.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			base := int64(id * 1000)
			num := "1380500" + pad4(id) + "0"
			for k := int64(0); k < 50; k++ {
				now := base + 2*k
				_, _ = r.RequestPort(num, 1+int64(k%2), 2+int64((k+1)%2), now, now)
				_, _ = r.Query(num, 3, route.ACQ, now+1)
				_, _ = r.QueryAt(num, 3, route.OR, 0)
			}
		}(w)
	}
	wg.Wait()
	// History at t=1 must be unchanged by all later concurrent operations.
	res, err := r.QueryAt("13805000000", 4, route.OR, 0)
	if err != nil || res.Home != 1 || res.Server != 1 || res.Ported || len(res.Path) != 1 {
		t.Fatalf("post-concurrency state: %+v err=%v", res, err)
	}
}

func pad4(id int) string {
	b := []byte("0000")
	for i := 3; i >= 0; i-- {
		b[i] = byte('0' + id%10)
		id /= 10
	}
	return string(b)
}
