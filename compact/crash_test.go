package compact

import (
	"fmt"
	"ontology/level"
	"ontology/segment"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type expect struct{ val, state string }

func setupStore(t *testing.T) (*level.Store, map[string]expect) {
	t.Helper()
	s, err := level.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var l0, l1 [][3]string
	for i := 0; i < 6; i++ {
		l0 = append(l0, [3]string{fmt.Sprintf("k%03d", i*2), fmt.Sprintf("v%03d", i*2), ""})
		l1 = append(l1, [3]string{fmt.Sprintf("k%03d", i*2+1), fmt.Sprintf("v%03d", i*2+1), ""})
	}
	ingest(t, s, 1, l1...)
	ingest(t, s, 0, l0...)
	exp := map[string]expect{}
	for i := 0; i < 14; i++ {
		k := fmt.Sprintf("k%03d", i)
		v, st, _ := s.Get([]byte(k))
		exp[k] = expect{string(v), fmt.Sprint(st)}
	}
	return s, exp
}

func assertReads(t *testing.T, s *level.Store, exp map[string]expect) {
	t.Helper()
	for k, e := range exp {
		v, st, err := s.Get([]byte(k))
		if err != nil || string(v) != e.val || fmt.Sprint(st) != e.state {
			t.Fatalf("Get(%s)=(%q,%v,%v), want (%q,%s)", k, v, st, err, e.val, e.state)
		}
	}
}

// 合并中途崩溃：对临时文件逐字节截断，恢复后旧段可用、读取逐字节相同。
func TestCrashMidCompact(t *testing.T) {
	full, _ := setupStore(t)
	if _, err := Compact(full, []int{0, 1}, 1); err != nil {
		t.Fatal(err)
	}
	var segPath string
	for _, ms := range full.Snapshot() {
		for _, m := range ms {
			segPath = m.Path
		}
	}
	fullBytes, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatal(err)
	}
	full.Close()
	for n := 1; n < len(fullBytes); n++ {
		s, e := setupStore(t)
		tmp := filepath.Join(filepath.Dir(s.Snapshot()[0][0].Path), "L1-000099.seg.tmp")
		if err := os.WriteFile(tmp, fullBytes[:n], 0o644); err != nil {
			t.Fatal(err)
		}
		s.Close()
		dir := filepath.Dir(tmp)
		s2, err := level.OpenStore(dir) // 恢复：清理 .tmp，旧段原样可用
		if err != nil {
			t.Fatalf("n=%d: reopen: %v", n, err)
		}
		if _, err := os.Stat(tmp); !os.IsNotExist(err) {
			t.Fatalf("n=%d: tmp not cleaned", n)
		}
		assertReads(t, s2, e)
		s2.Close()
	}
}

// 合并与读取并发：任意时刻点查都必须返回正确值，无空窗。
func TestConcurrentCompactRead(t *testing.T) {
	s, err := level.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const keys = 200
	want := func(i int) string { return fmt.Sprintf("v%04d", i) }
	for g := 0; g < 4; g++ {
		var kvs [][3]string
		for i := g * keys / 4; i < (g+1)*keys/4; i++ {
			kvs = append(kvs, [3]string{fmt.Sprintf("k%04d", i), want(i), ""})
		}
		ingest(t, s, 0, kvs...)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan string, 8)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				k := fmt.Sprintf("k%04d", (i+seed)%keys)
				v, st, err := s.Get([]byte(k))
				if err != nil || st != segment.StateValue || string(v) != want((i+seed)%keys) {
					errs <- fmt.Sprintf("Get(%s)=(%q,%v,%v)", k, v, st, err)
					return
				}
			}
		}(r)
	}
	if _, err := Compact(s, []int{0}, 1); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, 0, [3]string{"k9999", "v9999", ""})
	if _, err := Compact(s, []int{0, 1}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Compact(s, []int{1}, 2); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
