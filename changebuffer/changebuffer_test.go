package changebuffer

import (
	"fmt"
	"sync"
	"testing"
)

func TestConstructionAndBasicBuffering(t *testing.T) {
	cb, err := New(1024, 3, 150)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb.Op(7, Insert, []byte("a"), 100); err != nil {
		t.Fatal(err)
	}
	target := cb.pages[7]
	if target.inPool || len(target.queue) != 1 || target.bufBytes != 100 {
		t.Fatalf("unexpected page state: %+v", target)
	}
}

func TestLocalAndGlobalCreditBoundaries(t *testing.T) {
	cb := mustNew(t, 1024, 5, 150)
	mustOp(t, cb, 7, Insert, "a", 128)
	if got := cb.pages[7].bufBytes; got != 128 {
		t.Fatalf("bufBytes = %d, want 128", got)
	}
	if err := cb.Op(7, Insert, []byte("b"), 1); err != nil {
		t.Fatal(err)
	}
	if !cb.pages[7].inPool {
		t.Fatal("one byte past local lower bound should force merge")
	}

	cb = mustNew(t, 1024, 5, 150)
	mustOp(t, cb, 8, Insert, "x", 100)
	mustOp(t, cb, 9, Insert, "y", 50)
	if cb.globalBuf != 150 {
		t.Fatalf("globalBuf = %d, want 150", cb.globalBuf)
	}
	if err := cb.Op(10, Insert, []byte("z"), 1); err != nil {
		t.Fatal(err)
	}
	if !cb.pages[10].inPool || cb.globalBuf != 150 {
		t.Fatalf("forced global merge should release its reservation, globalBuf=%d", cb.globalBuf)
	}
	if err := cb.Op(11, Insert, []byte("w"), 1); err != nil {
		t.Fatal(err)
	}
	if !cb.pages[11].inPool {
		t.Fatal("global limit should remain occupied by buffered pages")
	}
}

func TestBufferedBytesAccumulateAcrossWholeQueue(t *testing.T) {
	cb := mustNew(t, 1024, 5, 10_000)
	mustOp(t, cb, 1, Insert, "a", 64)
	mustOp(t, cb, 1, Insert, "b", 30)
	mustOp(t, cb, 1, Insert, "c", 34)
	target := cb.pages[1]
	if len(target.queue) != 3 || target.bufBytes != 128 {
		t.Fatalf("queue=%d bufBytes=%d, want 3 and 128", len(target.queue), target.bufBytes)
	}
	if err := cb.Op(1, Insert, []byte("d"), 1); err != nil {
		t.Fatal(err)
	}
	if !cb.pages[1].inPool || cb.pages[1].used != 129 {
		t.Fatalf("forced merge result used=%d, want 129", cb.pages[1].used)
	}
}

func TestDeleteMarkAndPurgeAreNotChargedButAreLimitedByKp(t *testing.T) {
	cb := mustNew(t, 1024, 2, 1)
	mustOp(t, cb, 1, Insert, "x", 1)
	mustOp(t, cb, 2, DeleteMark, "missing", 0)
	mustOp(t, cb, 2, Purge, "unmarked", 0)
	if cb.globalBuf != 1 || len(cb.pages[2].queue) != 2 {
		t.Fatalf("globalBuf=%d queue=%d, marks and purges should be free", cb.globalBuf, len(cb.pages[2].queue))
	}
	if err := cb.Op(2, DeleteMark, []byte("again"), 0); err != nil {
		t.Fatal(err)
	}
	if !cb.pages[2].inPool {
		t.Fatal("a queue already at Kp should force merge")
	}
}

func TestBucketBoundariesAndLowerBounds(t *testing.T) {
	cb := mustNew(t, 100, 10, 10_000)
	cases := []struct {
		used int64
		want int
		lb   int64
	}{
		{96, 1, 3},
		{93, 2, 6},
		{87, 3, 12},
	}
	for _, tc := range cases {
		pageNo := int(tc.used)
		mustOp(t, cb, pageNo, Insert, "seed", tc.used)
		mustNoErr(t, cb.Load(pageNo))
		mustNoErr(t, cb.Evict(pageNo))
		free := cb.pageSize - tc.used
		if got := bucket(cb.pageSize, free); got != tc.want {
			t.Fatalf("used=%d bucket(%d)=%d, want %d", tc.used, free, got, tc.want)
		}
		if got := bucketLowerBound(cb.pageSize, tc.want); got != tc.lb {
			t.Fatalf("lb(%d)=%d, want %d", tc.want, got, tc.lb)
		}
		mustOp(t, cb, pageNo, Insert, "exact", tc.lb)
		if err := cb.Op(pageNo, Insert, []byte("plus-one"), 1); err != nil {
			t.Fatal(err)
		}
		if !cb.pages[pageNo].inPool {
			t.Fatalf("used=%d: equal to lb should buffer and one more should force merge", tc.used)
		}
	}
}

func TestEntrySemanticsAndArrivalOrder(t *testing.T) {
	cb := mustNew(t, 1024, 10, 10_000)
	mustOp(t, cb, 1, Insert, "k", 100)
	mustNoErr(t, cb.Load(1))
	mustNoErr(t, cb.Evict(1))
	mustOp(t, cb, 1, DeleteMark, "k", 0)
	mustOp(t, cb, 1, Insert, "k", 90)
	if got := viewMap(t, cb, 1)["k"]; got != (entrySpec{size: 100}) {
		t.Fatalf("reinsert existing key = %+v, want size 100 and undeleted", got)
	}

	mustOp(t, cb, 2, Insert, "removed", 50)
	mustOp(t, cb, 2, DeleteMark, "removed", 0)
	mustOp(t, cb, 2, Purge, "removed", 0)
	mustOp(t, cb, 2, Insert, "replacement", 1024)
	if got := viewMap(t, cb, 2); len(got) != 1 || got["replacement"].size != 1024 {
		t.Fatalf("purge should release space before forced insert: %+v", got)
	}

	markThenInsert := mustNew(t, 1024, 10, 10_000)
	mustOp(t, markThenInsert, 3, Insert, "k", 10)
	mustNoErr(t, markThenInsert.Load(3))
	mustNoErr(t, markThenInsert.Evict(3))
	mustOp(t, markThenInsert, 3, DeleteMark, "k", 0)
	mustOp(t, markThenInsert, 3, Insert, "k", 90)
	if got := viewMap(t, markThenInsert, 3)["k"]; got != (entrySpec{size: 10}) {
		t.Fatalf("mark then insert = %+v, want undeleted", got)
	}

	insertThenMark := mustNew(t, 1024, 10, 10_000)
	mustOp(t, insertThenMark, 4, Insert, "k", 10)
	mustNoErr(t, insertThenMark.Load(4))
	mustNoErr(t, insertThenMark.Evict(4))
	mustOp(t, insertThenMark, 4, Insert, "k", 90)
	mustOp(t, insertThenMark, 4, DeleteMark, "k", 0)
	if got := viewMap(t, insertThenMark, 4)["k"]; got != (entrySpec{size: 10, deleted: true}) {
		t.Fatalf("insert then mark = %+v, want deleted", got)
	}

	noChanges := mustNew(t, 1024, 10, 10_000)
	mustOp(t, noChanges, 5, DeleteMark, "missing", 0)
	mustOp(t, noChanges, 5, Insert, "unmarked", 20)
	mustOp(t, noChanges, 5, Purge, "unmarked", 0)
	if got := viewMap(t, noChanges, 5)["unmarked"]; got != (entrySpec{size: 20}) {
		t.Fatalf("purge of unmarked key should do nothing: %+v", got)
	}
}

func TestRejectionLeavesAllStateUnchanged(t *testing.T) {
	cb := mustNew(t, 1024, 1, 10_000)
	mustOp(t, cb, 1, Insert, "k", 1000)
	mustNoErr(t, cb.Load(1))
	mustNoErr(t, cb.Evict(1))
	mustOp(t, cb, 1, DeleteMark, "k", 0)
	before := clonePage(cb.pages[1])
	err := cb.Op(1, Insert, []byte("new"), 30)
	if err != ErrPageOutOfSpace {
		t.Fatalf("forced merge err = %v, want ErrPageOutOfSpace", err)
	}
	assertPageEqual(t, cb.pages[1], before)
	if got := viewMap(t, cb, 1)["k"]; !got.deleted || got.size != 1000 {
		t.Fatalf("logical state changed after rejected force: %+v", got)
	}

	mustNoErr(t, cb.Load(1))
	before = clonePage(cb.pages[1])
	err = cb.Op(1, Insert, []byte("space"), 30)
	if err != ErrPageOutOfSpace {
		t.Fatalf("direct insert err = %v, want ErrPageOutOfSpace", err)
	}
	assertPageEqual(t, cb.pages[1], before)
	if got := cb.globalBuf; got != 0 {
		t.Fatalf("globalBuf = %d, want 0", got)
	}
}

func TestViewEqualsLoadAndEvictAllowsRebuffering(t *testing.T) {
	cb := mustNew(t, 1024, 10, 10_000)
	mustOp(t, cb, 1, Insert, "b", 20)
	mustOp(t, cb, 1, Insert, "a", 30)
	mustOp(t, cb, 1, DeleteMark, "a", 0)
	beforeView := viewMap(t, cb, 1)
	if got := viewMap(t, cb, 1); got["a"] != beforeView["a"] || len(got) != len(beforeView) {
		t.Fatal("View changed state")
	}
	mustNoErr(t, cb.Load(1))
	if len(cb.pages[1].queue) != 0 || cb.pages[1].bufBytes != 0 {
		t.Fatal("pooled pages must have empty queues and zero bufBytes")
	}
	loaded := viewMap(t, cb, 1)
	if len(loaded) != len(beforeView) {
		t.Fatalf("loaded len=%d, view len=%d", len(loaded), len(beforeView))
	}
	for key, item := range beforeView {
		if loaded[key] != item {
			t.Fatalf("loaded[%s]=%+v, view=%+v", key, loaded[key], item)
		}
	}
	mustNoErr(t, cb.Evict(1))
	mustOp(t, cb, 1, DeleteMark, "b", 0)
	if len(cb.pages[1].queue) != 1 || cb.pages[1].inPool || cb.pages[1].bufBytes != 0 {
		t.Fatal("evicted page should accept buffered operations again")
	}
}

func TestValidationAndReportedErrorOrder(t *testing.T) {
	cases := []func() error{
		func() error { _, err := New(63, 1, 1); return err },
		func() error { _, err := New(64, 0, 1); return err },
		func() error { _, err := New(64, 1, 0); return err },
	}
	for _, call := range cases {
		if err := call(); err != ErrInvalidArgument {
			t.Fatalf("construction err = %v, want ErrInvalidArgument", err)
		}
	}

	cb := mustNew(t, 1024, 10, 10_000)
	if err := cb.Op(1_000_001, Insert, []byte("k"), 1); err != ErrInvalidArgument {
		t.Fatalf("page validation err = %v", err)
	}
	if err := cb.Op(1, Insert, nil, 1); err != ErrInvalidArgument {
		t.Fatalf("empty key err = %v", err)
	}
	if err := cb.Op(1, Kind("bad"), []byte("k"), 0); err != ErrInvalidArgument {
		t.Fatalf("kind validation err = %v", err)
	}
	if err := cb.Op(1, Insert, []byte("k"), 0); err != ErrInvalidArgument {
		t.Fatalf("insert size err = %v", err)
	}
	if err := cb.Op(1, DeleteMark, []byte("k"), 1); err != ErrInvalidArgument {
		t.Fatalf("delete-mark size err = %v", err)
	}
	if err := cb.Evict(1_000_001); err != ErrInvalidArgument {
		t.Fatalf("evict invalid page err = %v", err)
	}
	if err := cb.Evict(1); err != ErrPageNotInPool {
		t.Fatalf("evict absent page err = %v", err)
	}
	if err := cb.Load(1_000_001); err != ErrInvalidArgument {
		t.Fatalf("load invalid page err = %v", err)
	}
	if _, err := cb.View(1_000_001); err != ErrInvalidArgument {
		t.Fatalf("view invalid page err = %v", err)
	}
}

func TestConcurrentOperations(t *testing.T) {
	cb := mustNew(t, 1024, 4, 1_000_000)
	var wait sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wait.Add(1)
		go func(page int) {
			defer wait.Done()
			for i := 0; i < 20; i++ {
				key := []byte(fmt.Sprintf("k%d", i))
				if err := cb.Op(page, Insert, key, 5); err != nil {
					t.Errorf("insert: %v", err)
					return
				}
				if _, err := cb.View(page); err != nil {
					t.Errorf("view: %v", err)
					return
				}
				if i%3 == 0 {
					if err := cb.Load(page); err != nil {
						t.Errorf("load: %v", err)
						return
					}
					if err := cb.Evict(page); err != nil {
						t.Errorf("evict: %v", err)
						return
					}
				}
			}
		}(worker)
	}
	wait.Wait()
	assertInvariants(t, cb)
}

type entrySpec struct {
	size    int64
	deleted bool
}

func mustNew(t *testing.T, pageSize int64, maxPerPage int, globalLimit int64) *ChangeBuffer {
	t.Helper()
	cb, err := New(pageSize, maxPerPage, globalLimit)
	if err != nil {
		t.Fatal(err)
	}
	return cb
}

func mustOp(t *testing.T, cb *ChangeBuffer, page int, kind Kind, key string, size int64) {
	t.Helper()
	if err := cb.Op(page, kind, []byte(key), size); err != nil {
		t.Fatal(err)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func viewMap(t *testing.T, cb *ChangeBuffer, page int) map[string]entrySpec {
	t.Helper()
	entries, err := cb.View(page)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]entrySpec, len(entries))
	for _, entry := range entries {
		result[string(entry.Key)] = entrySpec{size: entry.Size, deleted: entry.Deleted}
	}
	return result
}

func assertPageEqual(t *testing.T, got, want *page) {
	t.Helper()
	if got.inPool != want.inPool ||
		got.used != want.used ||
		got.bufBytes != want.bufBytes ||
		len(got.entries) != len(want.entries) ||
		len(got.queue) != len(want.queue) {
		t.Fatalf("page changed\ngot:  %+v entries=%v queue=%+v\nwant: %+v entries=%v queue=%+v",
			got, got.entries, got.queue, want, want.entries, want.queue)
	}
	for key, item := range want.entries {
		if got.entries[key] != item {
			t.Fatalf("entry %q = %+v, want %+v", key, got.entries[key], item)
		}
	}
	for i := range want.queue {
		if got.queue[i] != want.queue[i] {
			t.Fatalf("queue[%d] = %+v, want %+v", i, got.queue[i], want.queue[i])
		}
	}
}

func assertInvariants(t *testing.T, cb *ChangeBuffer) {
	t.Helper()
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var global int64
	for number, target := range cb.pages {
		var used int64
		for _, item := range target.entries {
			used += item.size
		}
		if used != target.used || used > cb.pageSize {
			t.Fatalf("page %d used=%d actual=%d S=%d", number, target.used, used, cb.pageSize)
		}
		var buffered int64
		for _, queued := range target.queue {
			if queued.kind == Insert {
				buffered += queued.size
			}
		}
		if buffered != target.bufBytes {
			t.Fatalf("page %d bufBytes=%d actual=%d", number, target.bufBytes, buffered)
		}
		if target.inPool {
			if len(target.queue) != 0 || target.bufBytes != 0 {
				t.Fatalf("pooled page %d has buffered work", number)
			}
		} else {
			if len(target.queue) > cb.maxPerPage {
				t.Fatalf("page %d queue len=%d > Kp=%d", number, len(target.queue), cb.maxPerPage)
			}
			free := cb.pageSize - target.used
			lb := bucketLowerBound(cb.pageSize, bucket(cb.pageSize, free))
			if target.bufBytes > lb {
				t.Fatalf("page %d buffered=%d > lb=%d", number, target.bufBytes, lb)
			}
			global += target.bufBytes
		}
	}
	if global != cb.globalBuf {
		t.Fatalf("globalBuf=%d actual=%d", cb.globalBuf, global)
	}
	if global > cb.globalLimit {
		t.Fatalf("globalBuf=%d > G=%d", global, cb.globalLimit)
	}
}
