package fsmapper

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrent：多 goroutine 并发增删改查。
// 可串行化通过全局互斥天然满足；这里主要由 -race 检测数据竞争，
// 并验证并发过程中不出现重复折叠键 / 越界。
func TestConcurrent(t *testing.T) {
	m, err := New(64, 2048, 100000)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const perWorker = 300
	var wg sync.WaitGroup

	root, err := m.Add(0, "rootdir", true)
	if err != nil {
		t.Fatal(err)
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("w%d-f%d", w, i)
				id, addErr := m.Add(root, name, i%7 == 0)
				if addErr != nil {
					t.Errorf("worker %d Add %q: %v", w, name, addErr)
					continue
				}
				if _, _, lerr := m.Lookup(root, name); lerr != nil {
					t.Errorf("worker %d Lookup %q: %v", w, name, lerr)
				}
				if _, perr := m.Path(id); perr != nil {
					t.Errorf("worker %d Path %d: %v", w, id, perr)
				}
				// 一半改名。
				if i%2 == 0 {
					newName := fmt.Sprintf("w%d-r%d", w, i)
					if rerr := m.Rename(root, name, newName); rerr != nil {
						t.Errorf("worker %d Rename: %v", w, rerr)
					} else {
						name = newName
					}
				}
				// 并发读 Names。
				if _, nerr := m.Names(root); nerr != nil {
					t.Errorf("worker %d Names: %v", w, nerr)
				}
				// 非目录条目删除；目录稍后统一删除。
				if i%7 != 0 {
					if rerr := m.Remove(root, name); rerr != nil {
						t.Errorf("worker %d Remove %q: %v", w, name, rerr)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	names, err := m.Names(root)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, nm := range names {
		k := asciiFold(nm)
		if keys[k] {
			t.Fatalf("duplicate fold key after concurrent ops: %q", k)
		}
		keys[k] = true
		if len(nm) > 64 {
			t.Fatalf("name exceeds MaxBytes: %q", nm)
		}
	}

	// 清空剩余目录条目后删除所有目录。
	for _, nm := range names {
		if rerr := m.Remove(root, nm); rerr != nil {
			t.Fatalf("cleanup %q: %v", nm, rerr)
		}
	}
	if err := m.Remove(0, "rootdir"); err != nil {
		t.Fatalf("remove rootdir: %v", err)
	}
	if final, _ := m.Names(0); len(final) != 0 {
		t.Fatalf("root not empty: %v", final)
	}
}
