package ranking

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// mustGet 取出三元组并打印输入、结果。
func mustGet(t *testing.T, lb *Leaderboard, id string) Triple {
	t.Helper()
	tr, ok := lb.Get(id)
	if !ok {
		t.Fatalf("Get(%q) 未找到元素", id)
	}
	t.Logf("查询 Get(%q) -> 位次=%d 排名=%d 稠密排名=%d", id, tr.Position, tr.Rank, tr.DenseRank)
	return tr
}

// expectTriple 断言三元组并打印判定依据。
func expectTriple(t *testing.T, lb *Leaderboard, id string, want Triple, why string) {
	t.Helper()
	got := mustGet(t, lb, id)
	if got != want {
		t.Fatalf("Get(%q) = %+v, 期望 %+v; 判定依据: %s", id, got, want, why)
	}
	t.Logf("判定通过: Get(%q) = %+v; 判定依据: %s", id, want, why)
}

func mustInsert(t *testing.T, lb *Leaderboard, id string, score int64) {
	t.Helper()
	if err := lb.Insert(id, score); err != nil {
		t.Fatalf("Insert(%q, %d) 意外失败: %v", id, score, err)
	}
	t.Logf("插入 Insert(%q, %d) 成功", id, score)
}

func mustDelete(t *testing.T, lb *Leaderboard, id string) {
	t.Helper()
	if err := lb.Delete(id); err != nil {
		t.Fatalf("Delete(%q) 意外失败: %v", id, err)
	}
	t.Logf("删除 Delete(%q) 成功", id)
}

func mustSelfCheck(t *testing.T, lb *Leaderboard) {
	t.Helper()
	if err := lb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
	t.Logf("SelfCheck 通过: 全序有序、位次双射、三元组满足定义")
}

// snapshot 抓取当前所有元素的三元组，用于失败前后状态比对。
func snapshot(t *testing.T, lb *Leaderboard, ids ...string) map[string]Triple {
	t.Helper()
	m := make(map[string]Triple, len(ids))
	for _, id := range ids {
		m[id] = mustGet(t, lb, id)
	}
	return m
}

// TestTieSharing 覆盖同分共享排名与稠密排名、位次按标识升序互异、
// 并列产生排名空洞而稠密排名无空洞。
func TestTieSharing(t *testing.T) {
	lb := New()
	t.Log("输入: (c,100) (a,100) (b,100) (d,90) (e,80)，乱序插入三个同分元素")
	mustInsert(t, lb, "c", 100)
	mustInsert(t, lb, "a", 100)
	mustInsert(t, lb, "b", 100)
	mustInsert(t, lb, "d", 90)
	mustInsert(t, lb, "e", 80)

	expectTriple(t, lb, "a", Triple{1, 1, 1}, "100 分最高，同分按标识升序 a 位次 1；同分共享排名 1、稠密排名 1")
	expectTriple(t, lb, "b", Triple{2, 1, 1}, "同分位次互异: b 位次 2；排名仍共享 1")
	expectTriple(t, lb, "c", Triple{3, 1, 1}, "同分位次互异: c 位次 3；排名仍共享 1")
	expectTriple(t, lb, "d", Triple{4, 4, 2}, "90 分前有 3 个严格更高元素，排名 4（并列产生空洞，跳过 2、3）；严格更高的互异分数仅 {100}，稠密排名 2 无空洞")
	expectTriple(t, lb, "e", Triple{5, 5, 3}, "80 分前有 4 个严格更高元素，排名 5；严格更高互异分数 {100,90}，稠密排名 3")
	mustSelfCheck(t, lb)
}

// TestPositionUnique 验证位次在 1..n 上构成双射。
func TestPositionUnique(t *testing.T) {
	lb := New()
	ids := []string{}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("u%02d", i)
		// 刻意让分数大量重复，位次仍须互异。
		mustInsert(t, lb, id, int64(i%7))
		ids = append(ids, id)
	}
	seen := make(map[int]string)
	for _, id := range ids {
		tr := mustGet(t, lb, id)
		if tr.Position < 1 || tr.Position > len(ids) {
			t.Fatalf("位次 %d 超出 [1,%d]", tr.Position, len(ids))
		}
		if prev, dup := seen[tr.Position]; dup {
			t.Fatalf("位次 %d 被 %q 与 %q 共享，违反双射", tr.Position, prev, id)
		}
		seen[tr.Position] = id
	}
	t.Logf("判定通过: %d 个元素的位次构成 [1,%d] 上的双射", len(ids), len(ids))
	mustSelfCheck(t, lb)
}

// TestInsertAdjustsLower 验证插入后分数严格更低元素的位次与排名加一，
// 稠密排名仅当引入新的更高分数取值时加一。
func TestInsertAdjustsLower(t *testing.T) {
	lb := New()
	mustInsert(t, lb, "low", 10)
	mustInsert(t, lb, "mid", 20)
	expectTriple(t, lb, "low", Triple{2, 2, 2}, "基准: 仅 20 分严格更高")

	mustInsert(t, lb, "mid2", 20)
	t.Log("判定依据: 插入已有分数 20，未引入新分数取值，低分元素稠密排名不变，位次与排名加一")
	expectTriple(t, lb, "low", Triple{3, 3, 2}, "位次/排名 +1，稠密排名不变")
	expectTriple(t, lb, "mid", Triple{1, 1, 1}, "同分共享排名 1，位次按标识升序")
	expectTriple(t, lb, "mid2", Triple{2, 1, 1}, "同分共享排名 1")

	mustInsert(t, lb, "high", 30)
	t.Log("判定依据: 插入新分数 30，引入新的更高分数取值，所有更低元素位次/排名/稠密排名均加一")
	expectTriple(t, lb, "low", Triple{4, 4, 3}, "三者均 +1")
	expectTriple(t, lb, "mid", Triple{2, 2, 2}, "三者均 +1")
	expectTriple(t, lb, "mid2", Triple{3, 2, 2}, "排名与 mid 共享 2")
	expectTriple(t, lb, "high", Triple{1, 1, 1}, "最高分居首")
	mustSelfCheck(t, lb)
}

// TestDeleteAdjustsLower 验证删除的对称调整：位次与排名减一，
// 稠密排名仅当被删元素是其分数上的最后一个元素时才减一。
func TestDeleteAdjustsLower(t *testing.T) {
	lb := New()
	mustInsert(t, lb, "a", 30)
	mustInsert(t, lb, "b", 30)
	mustInsert(t, lb, "c", 20)
	mustInsert(t, lb, "d", 10)

	mustDelete(t, lb, "a")
	t.Log("判定依据: 30 分上仍有 b，删除 a 不消除分数取值，低分元素稠密排名不变，位次/排名减一")
	expectTriple(t, lb, "c", Triple{2, 2, 2}, "位次/排名 -1，稠密排名不变")
	expectTriple(t, lb, "d", Triple{3, 3, 3}, "位次/排名 -1，稠密排名不变")

	mustDelete(t, lb, "b")
	t.Log("判定依据: b 是 30 分上最后一个元素，删除后分数取值消失，低分元素稠密排名减一")
	expectTriple(t, lb, "c", Triple{1, 1, 1}, "30 分取值消失，c 成为最高")
	expectTriple(t, lb, "d", Triple{2, 2, 2}, "三者均 -1")
	mustSelfCheck(t, lb)
}

// TestInvalidInputs 验证三类非法输入被互不相同的错误拒绝，
// 且失败后所有元素三元组不变，拒绝后仍可正常使用。
func TestInvalidInputs(t *testing.T) {
	lb := New()
	mustInsert(t, lb, "x", 100)
	mustInsert(t, lb, "y", 50)
	before := snapshot(t, lb, "x", "y")

	checkUnchanged := func(step string) {
		t.Helper()
		after := snapshot(t, lb, "x", "y")
		for id, tr := range before {
			if after[id] != tr {
				t.Fatalf("%s 后 %q 的三元组由 %+v 变为 %+v，违反失败不改变状态", step, id, tr, after[id])
			}
		}
		if lb.Len() != 2 {
			t.Fatalf("%s 后元素个数变为 %d，违反失败不改变状态", step, lb.Len())
		}
		t.Logf("判定通过: %s 被拒后所有三元组与元素个数不变", step)
	}

	err := lb.Insert("", 1)
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Insert(空标识) 错误 = %v, 期望 ErrEmptyID", err)
	}
	t.Logf("Insert(\"\", 1) 被拒: %v", err)
	checkUnchanged("Insert(空标识)")

	err = lb.Insert("x", 999)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("Insert(已存在标识) 错误 = %v, 期望 ErrDuplicateID", err)
	}
	t.Logf("Insert(\"x\", 999) 被拒: %v", err)
	checkUnchanged("Insert(已存在标识)")

	err = lb.Delete("")
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Delete(空标识) 错误 = %v, 期望 ErrEmptyID", err)
	}
	t.Logf("Delete(\"\") 被拒: %v", err)
	checkUnchanged("Delete(空标识)")

	err = lb.Delete("ghost")
	if !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("Delete(不存在标识) 错误 = %v, 期望 ErrIDNotFound", err)
	}
	t.Logf("Delete(\"ghost\") 被拒: %v", err)
	checkUnchanged("Delete(不存在标识)")

	// 三类错误互不相同、可判定。
	if errors.Is(ErrEmptyID, ErrDuplicateID) || errors.Is(ErrEmptyID, ErrIDNotFound) || errors.Is(ErrDuplicateID, ErrIDNotFound) {
		t.Fatal("三类错误必须互不相同")
	}
	t.Log("判定通过: ErrEmptyID / ErrDuplicateID / ErrIDNotFound 互不相同且可用 errors.Is 判定")

	// 被拒后仍可继续正常使用。
	mustInsert(t, lb, "z", 75)
	expectTriple(t, lb, "z", Triple{2, 2, 2}, "75 分介于 100 与 50 之间")
	mustDelete(t, lb, "x")
	expectTriple(t, lb, "z", Triple{1, 1, 1}, "删除 100 分后 z 居首")
	mustSelfCheck(t, lb)
}

// TestConcurrentInsert 验证并发插入互异元素后位次构成双射，
// 且期间任一时刻读到的已插入元素排名单调不减。
func TestConcurrentInsert(t *testing.T) {
	lb := New()
	const n = 200
	// 基准元素分数最低，之后所有并发插入的分数都严格更高，
	// 因此 pivot 的排名只会被推高，读取序列必须单调不减。
	mustInsert(t, lb, "pivot", 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 读协程: 持续读取 pivot 的排名，断言单调不减。
	readDone := make(chan error, 1)
	go func() {
		prev := 0
		for {
			select {
			case <-stop:
				readDone <- nil
				return
			default:
			}
			tr, ok := lb.Get("pivot")
			if !ok {
				readDone <- fmt.Errorf("pivot 在并发读取期间丢失")
				return
			}
			if tr.Rank < prev {
				readDone <- fmt.Errorf("pivot 排名由 %d 降为 %d，违反单调不减", prev, tr.Rank)
				return
			}
			prev = tr.Rank
		}
	}()

	// 写协程: 并发插入互异元素。
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := lb.Insert(fmt.Sprintf("w%03d", i), int64(i+1)); err != nil {
				t.Errorf("并发 Insert 失败: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	t.Log("判定通过: 并发插入期间 pivot 排名读取序列单调不减")

	// 并发查询与自检。
	var qwg sync.WaitGroup
	for i := 0; i < 8; i++ {
		qwg.Add(1)
		go func() {
			defer qwg.Done()
			for j := 0; j < 50; j++ {
				lb.Get("pivot")
				lb.Len()
				if err := lb.SelfCheck(); err != nil {
					t.Errorf("并发 SelfCheck 失败: %v", err)
					return
				}
			}
		}()
	}
	qwg.Wait()

	// 位次必须在 [1, n+1] 上构成双射。
	if lb.Len() != n+1 {
		t.Fatalf("元素个数 = %d, 期望 %d", lb.Len(), n+1)
	}
	positions := make([]int, 0, n+1)
	positions = append(positions, mustGet(t, lb, "pivot").Position)
	for i := 0; i < n; i++ {
		tr, ok := lb.Get(fmt.Sprintf("w%03d", i))
		if !ok {
			t.Fatalf("w%03d 插入后丢失", i)
		}
		positions = append(positions, tr.Position)
	}
	sort.Ints(positions)
	for i, p := range positions {
		if p != i+1 {
			t.Fatalf("位次集合 %v 不是 [1,%d] 的双射", positions, n+1)
		}
	}
	t.Logf("判定通过: %d 个并发插入元素的位次构成 [1,%d] 上的双射", n+1, n+1)
	mustSelfCheck(t, lb)
}
