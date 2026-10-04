package ontology_test

import (
	"strconv"
	"sync"
	"testing"

	"ontology/adjust"
	"ontology/authz"
	"ontology/count"
)

func view(t *testing.T, db *adjust.DB, loc string) (int64, int64, int64) {
	t.Helper()
	b, m, p, err := db.View([]byte(loc))
	if err != nil {
		t.Fatal(err)
	}
	return b, m, p
}

// TestConcurrentMoveAndCount：盘点轮次因“库位唯一占用”而串行，
// 8 路 Move 与之高度并发。每轮记录开始时的 mv 快照，结束时用 mv 差量
// 精确分离出该轮调整量，最终校验 book = 初始 + mv + 调整量之和。
func TestConcurrentMoveAndCount(t *testing.T) {
	const initial = int64(1_000_000)
	db := adjust.New(0, 100, 1<<60)
	if err := db.AddLoc([]byte("L"), initial, 10); err != nil {
		t.Fatal(err)
	}
	eng := count.New(db)
	mgr := authz.New(eng)
	if err := mgr.Grant([]byte("boss"), true, true); err != nil {
		t.Fatal(err)
	}

	var adjMu sync.Mutex
	var adjSum int64
	cycleSem := make(chan struct{}, 1)

	const rounds = 200
	var wg sync.WaitGroup
	for r := 0; r < rounds; r++ {
		task := []byte("t" + strconv.Itoa(r))
		wg.Add(1)
		go func(task []byte) {
			defer wg.Done()
			cycleSem <- struct{}{}
			defer func() { <-cycleSem }()
			if err := eng.Open(task, [][]byte{[]byte("L")}); err != nil {
				return
			}
			book0, mv0, _ := view(t, db, "L")
			// tpct=100：任何非负实盘都在容差内，初盘必采纳。
			if err := eng.Submit(task, []byte("L"), book0, []byte("c"+string(task))); err != nil {
				t.Errorf("submit: %v", err)
			}
			if ph, _ := eng.Phase(task, []byte("L")); ph == count.Pending {
				_ = mgr.Approve(task, []byte("L"), []byte("boss"))
			}
			book1, mv1, _ := view(t, db, "L")
			if err := eng.Close(task); err != nil {
				t.Errorf("close: %v", err)
			}
			// book1-book0 = (mv1-mv0) + 本轮调整
			adjMu.Lock()
			adjSum += book1 - book0 - (mv1 - mv0)
			adjMu.Unlock()
		}(task)
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 5000; k++ {
				d := int64(1)
				if (k+seed)%2 == 0 {
					d = -1
				}
				if err := db.Move([]byte("L"), d); err != nil {
					t.Errorf("move: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	book, err := db.Book([]byte("L"))
	if err != nil {
		t.Fatal(err)
	}
	mv, err := db.Moved([]byte("L"))
	if err != nil {
		t.Fatal(err)
	}
	if book < 0 {
		t.Fatalf("book negative: %d", book)
	}
	if want := initial + mv + adjSum; book != want {
		t.Fatalf("invariant broken: book=%d want=%d (mv=%d adj=%d)", book, want, mv, adjSum)
	}
}
