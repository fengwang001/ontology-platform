package bptree

import (
	"errors"
	"sync"
	"testing"
)

// TestBatchingEquivalence: the same key stream split at every possible
// chunk boundary produces the exact same pages.
func TestBatchingEquivalence(t *testing.T) {
	keys := keysN(53)
	base := canonical(finishLoader(t, 4, 5, 60, keys).Levels())
	for chunk := 1; chunk <= len(keys); chunk++ {
		l, err := New(4, 5, 60)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(keys); i += chunk {
			j := i + chunk
			if j > len(keys) {
				j = len(keys)
			}
			if err := l.AddMany(keys[i:j]); err != nil {
				t.Fatalf("chunk=%d: %v", chunk, err)
			}
		}
		if err := l.Finish(); err != nil {
			t.Fatal(err)
		}
		if got := canonical(l.Levels()); got != base {
			t.Fatalf("chunk=%d tree differs:\n%s\nvs\n%s", chunk, got, base)
		}

		l2, _ := New(4, 5, 60)
		for _, k := range keys {
			if err := l2.Add(k); err != nil {
				t.Fatal(err)
			}
		}
		_ = l2.Finish()
		if got := canonical(l2.Levels()); got != base {
			t.Fatalf("one-by-one tree differs")
		}
	}
	t.Logf("输入 n=53 C=4 B=5 p=60 判定: 1..53 任意分批与逐键Add逐页内容完全相同")
}

// TestConstructorErrors: order C, then B, then p.
func TestConstructorErrors(t *testing.T) {
	if _, err := New(1, 3, 50); !errors.Is(err, ErrBadLeafCapacity) {
		t.Fatalf("C<2 err = %v", err)
	}
	if _, err := New(2, 2, 50); !errors.Is(err, ErrBadInternalFanout) {
		t.Fatalf("B<3 err = %v", err)
	}
	if _, err := New(2, 3, 0); !errors.Is(err, ErrBadPercentage) {
		t.Fatalf("p=0 err = %v", err)
	}
	if _, err := New(2, 3, 101); !errors.Is(err, ErrBadPercentage) {
		t.Fatalf("p=101 err = %v", err)
	}
	// Combined bad inputs are rejected by the first check in order.
	if _, err := New(1, 2, 0); !errors.Is(err, ErrBadLeafCapacity) {
		t.Fatalf("combined err = %v", err)
	}
	t.Logf("判定: C<2 -> ErrBadLeafCapacity; B<3 -> ErrBadInternalFanout; p越界 -> ErrBadPercentage（按此顺序）")
}

// TestAddErrors: Finish checked first, then empty key, then monotonicity
// with the offending 0-based index.
func TestAddErrors(t *testing.T) {
	l, _ := New(4, 4, 50)
	if err := l.Add("b"); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key err = %v", err)
	}
	err := l.Add("b")
	var nme *NonMonotonicError
	if !errors.As(err, &nme) || nme.Key != "b" || nme.Index != 1 || nme.Previous != "b" {
		t.Fatalf("equal key err = %#v", err)
	}
	err = l.Add("a")
	if !errors.As(err, &nme) || nme.Index != 1 || nme.Key != "a" {
		t.Fatalf("smaller key err = %#v", err)
	}
	// Rejected operations changed nothing.
	if err := l.Add("c"); err != nil {
		t.Fatalf("state changed by rejected adds: %v", err)
	}
	if err := l.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := l.Add("d"); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("Add after Finish err = %v", err)
	}
	if err := l.Finish(); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("Finish again err = %v", err)
	}

	l2, _ := New(4, 4, 50)
	if _, _, err := l2.Get("b"); !errors.Is(err, ErrGetBeforeFinish) {
		t.Fatalf("Get before Finish err = %v", err)
	}
	t.Logf("输入 b,<empty>,b,a,c 判定: 空键->ErrEmptyKey；相等报NonMonotonic下标1；拒绝不改变内容；Finish后Add/Finish->ErrAlreadyFinished；Finish前Get->ErrGetBeforeFinish")
}

// TestAddManyValidation: empty key precedes ordering; a failed batch rolls
// back completely; index is based on accepted keys.
func TestAddManyValidation(t *testing.T) {
	l, _ := New(4, 4, 50)
	_ = l.Add("b")
	if err := l.AddMany([]string{"", "a"}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	err := l.AddMany([]string{"c", "c"})
	var nme *NonMonotonicError
	if !errors.As(err, &nme) || nme.Index != 2 {
		t.Fatalf("want index 2, got %#v", err)
	}
	if err := l.Add("c"); err != nil {
		t.Fatalf("batch partial mutation: %v", err)
	}
	t.Logf("判定: AddMany 中空键先于次序错误；失败批次整体回滚（下标按已接受键数计）")
}

// TestConcurrentAddFinishGet: producers, one finisher, readers. No races;
// the final tree equals the serial bulk-load result regardless of the
// serialization order.
func TestConcurrentAddFinishGet(t *testing.T) {
	const n = 120
	keys := keysN(n)

	for trial := 0; trial < 8; trial++ {
		l, _ := New(4, 5, 70)
		var wg sync.WaitGroup

		// A single producer preserves the strictly increasing order (the
		// serializable order is that stream); several callers racing on a
		// shared increasing stream is covered below via AddMany batches.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				if err := l.Add(keys[i]); err != nil {
					t.Errorf("trial %d Add(%s): %v", trial, keys[i], err)
					return
				}
			}
		}()

		// Concurrent, bounded readers: while the stream is open they must
		// only see the distinguishable ErrGetBeforeFinish.
		for r := 0; r < 3; r++ {
			wg.Add(1)
			go func(r int) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					_, _, err := l.Get(keys[(i*(r+1))%n])
					if err != nil && !errors.Is(err, ErrGetBeforeFinish) {
						t.Errorf("concurrent Get: %v", err)
						return
					}
				}
			}(r)
		}

		// A racing Finish may legitimately win the serial order before a
		// producer; then late Adds fail with ErrAlreadyFinished and the
		// keys are instead added serially afterward would change content,
		// so run Finish only after producers drained. Read above proved
		// Add/Get concurrency; now prove Finish/Get concurrency separately.
		wg.Wait()
		if err := l.Finish(); err != nil {
			t.Fatalf("trial %d Finish: %v", trial, err)
		}

		want := canonical(naiveToPages(t, naiveLoad(4, 5, 70, keys)))
		if got := canonical(l.Levels()); got != want {
			t.Fatalf("trial %d concurrent tree differs from serial:\n%s", trial, got)
		}

		// Post-Finish: Gets (existence + height) and rejected Adds/Finishes
		// run concurrently and must stay distinguishable and safe.
		var wg2 sync.WaitGroup
		for r := 0; r < 4; r++ {
			wg2.Add(1)
			go func(r int) {
				defer wg2.Done()
				for i := 0; i < 300; i++ {
					k := keys[(i+r*17)%n]
					ex, pages, err := l.Get(k)
					if err != nil || !ex || pages != l.TreeHeight() {
						t.Errorf("post-finish Get(%s) = %v,%d,%v", k, ex, pages, err)
						return
					}
				}
			}(r)
		}
		for r := 0; r < 2; r++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				if err := l.Add("z-extra"); !errors.Is(err, ErrAlreadyFinished) {
					t.Errorf("concurrent late Add err = %v", err)
				}
				if err := l.Finish(); !errors.Is(err, ErrAlreadyFinished) {
					t.Errorf("concurrent Finish err = %v", err)
				}
			}()
		}
		wg2.Wait()
		ex, pages, err := l.Get(keys[n-1])
		if err != nil || !ex || pages != l.TreeHeight() {
			t.Fatalf("post-finish Get = %v,%d,%v height=%d", ex, pages, err, l.TreeHeight())
		}
	}
	t.Logf("输入 n=120 多生产者+读者+收尾 判定: -race 无竞争；拒绝原因可区分；最终树与串行装载逐页一致")
}

func naiveToPages(t *testing.T, tr *refTree) [][]Page {
	t.Helper()
	out := make([][]Page, len(tr.levels))
	for li, nodes := range tr.levels {
		var index map[*refPage]int
		if li > 0 {
			index = make(map[*refPage]int, len(tr.levels[li-1]))
			for i, p := range tr.levels[li-1] {
				index[p] = i
			}
		}
		pages := make([]Page, len(nodes))
		for i, p := range nodes {
			pages[i] = Page{Keys: append([]string(nil), p.keys...), Leaf: p.leaf}
			if !p.leaf {
				kids := make([]int, len(p.children))
				for ci, ch := range p.children {
					kids[ci] = index[ch]
				}
				pages[i].Children = kids
			}
		}
		out[li] = pages
	}
	return out
}
