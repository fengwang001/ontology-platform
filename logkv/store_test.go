package logkv

import (
	"fmt"
	"sync"
	"testing"
)

func TestBasicPutGetDelete(t *testing.T) {
	s := newTestStore(t, 1<<20)
	defer s.Close()
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
	mustStatus(t, s, "c", StatusNotFound)
	mustDelete(t, s, "a")
	mustStatus(t, s, "a", StatusDeleted)
	mustStatus(t, s, "c", StatusNotFound) // 已删除与从未出现可区分
	mustPut(t, s, "a", "3")
	mustGet(t, s, "a", "3")
}

func TestEmptyKeyRejected(t *testing.T) {
	s := newTestStore(t, 1<<20)
	defer s.Close()
	if err := s.Put(nil, []byte("v")); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("put empty key: %v", err)
	}
	if err := s.Delete(nil); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("delete empty key: %v", err)
	}
	if _, _, err := s.Get(nil); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("get empty key: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	if _, err := Open(Config{Dir: "", MaxSegmentBytes: 1}); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("empty dir: %v", err)
	}
	if _, err := Open(Config{Dir: t.TempDir(), MaxSegmentBytes: 0}); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("zero max segment: %v", err)
	}
}

func TestRotationAndReopen(t *testing.T) {
	s := newTestStore(t, 128) // 小段强制轮转
	for i := 0; i < 20; i++ {
		mustPut(t, s, fmt.Sprintf("k%02d", i), fmt.Sprintf("v%02d", i))
	}
	mustDelete(t, s, "k00")
	sealed, active := segIDs(t, s)
	if len(sealed) == 0 {
		t.Fatalf("expected rotation, active=%d", active)
	}
	s = reopen(t, s)
	defer s.Close()
	for i := 1; i < 20; i++ {
		mustGet(t, s, fmt.Sprintf("k%02d", i), fmt.Sprintf("v%02d", i))
	}
	mustStatus(t, s, "k00", StatusDeleted)
}

func TestSeqMonotonicAcrossReopen(t *testing.T) {
	s := newTestStore(t, 1<<20)
	mustPut(t, s, "a", "1")
	mustDelete(t, s, "a")
	next := s.NextSeq()
	s = reopen(t, s)
	defer s.Close()
	if got := s.NextSeq(); got != next {
		t.Fatalf("next seq after reopen = %d, want %d", got, next)
	}
	mustPut(t, s, "b", "2")
	if got := s.NextSeq(); got != next+1 {
		t.Fatalf("next seq after put = %d, want %d", got, next+1)
	}
}

func TestDeletedDistinctFromNeverSeen(t *testing.T) {
	s := newTestStore(t, 1<<20)
	defer s.Close()
	mustPut(t, s, "gone", "x")
	mustDelete(t, s, "gone")
	mustStatus(t, s, "gone", StatusDeleted)
	mustStatus(t, s, "never", StatusNotFound)
	s2 := reopen(t, s)
	defer s2.Close()
	mustStatus(t, s2, "gone", StatusDeleted)
	mustStatus(t, s2, "never", StatusNotFound)
}

func TestConcurrentReadWrite(t *testing.T) {
	s := newTestStore(t, 4096)
	defer s.Close()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("key-%d-%d", w, i%10)
				if err := s.Put([]byte(key), []byte(fmt.Sprintf("val-%d", i))); err != nil {
					t.Errorf("put: %v", err)
					return
				}
				if _, _, err := s.Get([]byte(key)); err != nil {
					t.Errorf("get: %v", err)
					return
				}
				if i%7 == 0 {
					if err := s.Delete([]byte(key)); err != nil {
						t.Errorf("delete: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
}
