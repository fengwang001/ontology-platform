package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/snap"
)

func key(i int) string { return fmt.Sprintf("k%05d", i) }

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// setup 建库、种 n 个键、拍快照；drain 导出全部块并拼接。
func setup(t *testing.T, chunk, n int) *DB {
	t.Helper()
	db, _ := New(chunk)
	for i := 0; i < n; i++ {
		_ = db.Put(key(i), int64(i))
	}
	db.Snapshot()
	return db
}

func drain(t *testing.T, db *DB) []snap.Entry {
	t.Helper()
	var all []snap.Entry
	cur := ""
	for {
		en, nc, done, err := db.Next(cur)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, en...)
		cur = nc
		if done {
			return all
		}
	}
}

func checkFull(t *testing.T, got []snap.Entry, n int) {
	t.Helper()
	if len(got) != n {
		t.Fatalf("导出 %d 条，want %d", len(got), n)
	}
	for i, e := range got {
		if e.Key != key(i) || e.Val != int64(i) {
			t.Fatalf("第%d条=%v，与快照时刻不符", i, e)
		}
	}
}

func TestInvariantNaiveRef(t *testing.T) {
	for _, n := range []int{0, 1, 5, 100, 1000} {
		for _, chunk := range []int{1, 3, 64} {
			checkFull(t, drain(t, setup(t, chunk, n)), n)
		}
	}
}

func TestInvariantNoDupNoGap(t *testing.T) {
	got := drain(t, setup(t, 7, 500))
	seen, prev := map[string]bool{}, ""
	for i, e := range got {
		if seen[e.Key] || (i > 0 && e.Key <= prev) {
			t.Fatalf("重复或倒退：%q", e.Key)
		}
		seen[e.Key], prev = true, e.Key
	}
	if len(seen) != 500 {
		t.Fatalf("并集 %d != 500", len(seen))
	}
}

func TestInvariantPointInTime(t *testing.T) {
	db := setup(t, 2, 6)
	_ = db.Put("k00002", 99) // 覆盖已有键
	_ = db.Del("k00003")     // 删除已有键
	_ = db.Put("zz", 1)      // 新增键
	checkFull(t, drain(t, db), 6)
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	_, err := New(0)
	wantErr(t, err, ErrBadChunk)
	db, _ := New(2)
	_, _, _, err = db.Next("")
	wantErr(t, err, ErrNoSnapshot)
	for i := 0; i < 4; i++ {
		_ = db.Put(key(i), int64(i))
	}
	db.Snapshot()
	before := fmt.Sprint(db.View())
	wantErr(t, db.Put("", 1), ErrEmptyKey)
	wantErr(t, db.Del(""), ErrEmptyKey)
	if fmt.Sprint(db.View()) != before {
		t.Fatal("空键被拒后实时状态改变")
	}
	drain(t, db)
	for _, c := range []string{"k00001", ""} { // 完成后 Next/Resume 都拒绝
		_, _, _, err = db.Next(c)
		wantErr(t, err, ErrFinished)
	}
	wantErr(t, db.Put("ok", 1), nil)
}

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{ErrBadChunk, ErrEmptyKey, ErrNoSnapshot, ErrFinished}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("哨兵 %d 与 %d 相同", i, j)
			}
		}
	}
}

func TestConcurrentPointInTime(t *testing.T) {
	db := setup(t, 16, 256)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 256; i++ {
				_ = db.Put(fmt.Sprintf("w%d-%05d", w, i), int64(i))
			}
		}(w)
	}
	close(start)
	got := drain(t, db)
	wg.Wait()
	checkFull(t, got, 256)
}

func TestSelfCheck(t *testing.T) {
	if err := (&DB{}).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
