package locker

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentPickupExactlyOneSuccess：同一取件码被并发取件，恰好一次成功。
func TestConcurrentPickupExactlyOneSuccess(t *testing.T) {
	cfg := testCfg()
	cfg.CodeCount = 50
	cells := make([]Cell, 50)
	for i := range cells {
		cells[i] = Cell{ID: CellID(i + 1), Size: SizeSmall}
	}
	c, err := NewCabinet(cells, cfg, NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Deposit(0, "C1", SizeSmall, "13800001111")
	if err != nil {
		t.Fatal(err)
	}

	const n = 64
	var wg sync.WaitGroup
	var success int32
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 全部使用同一时刻 t=1（不小于时钟即可）。
			if _, err := c.Pickup(1, r.Code, "13800001111"); err == nil {
				atomic.AddInt32(&success, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("concurrent pickup successes=%d want exactly 1", success)
	}
}

// TestConcurrentDepositsNoCellSharing：并发存件下绝不出现两快件共用格口，
// 且成功数等于成功结果中不同格口数。
func TestConcurrentDepositsNoCellSharing(t *testing.T) {
	cfg := testCfg()
	cfg.CodeCount = 200
	cells := make([]Cell, 100)
	for i := range cells {
		cells[i] = Cell{ID: CellID(i + 1), Size: SizeSmall}
	}
	c, err := NewCabinet(cells, cfg, NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}

	const n = 150 // 多于格口数，必然有部分收到 ErrAllFittingBusy
	var wg sync.WaitGroup
	var ok int32
	var mu sync.Mutex
	used := make(map[CellID]bool)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		tracking := TrackingNo("P" + itoa(i))
		go func(tr TrackingNo) {
			defer wg.Done()
			<-start
			res, err := c.Deposit(1, tr, SizeSmall, "13800002222")
			if err == nil {
				atomic.AddInt32(&ok, 1)
				mu.Lock()
				used[res.Cell] = true
				mu.Unlock()
			}
		}(tracking)
	}
	close(start)
	wg.Wait()

	if int(ok) != 100 {
		t.Fatalf("successful deposits=%d want 100", ok)
	}
	if len(used) != 100 {
		t.Fatalf("distinct occupied cells=%d want 100 (no sharing)", len(used))
	}
	if len(c.Occupied()) != 100 {
		t.Fatalf("occupied=%d want 100", len(c.Occupied()))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
