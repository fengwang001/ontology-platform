package liveness

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func codeOf(err error) ErrorCode {
	var ae *AnalysisError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestAddValidationPriority(t *testing.T) {
	a := NewAnalyzer()

	err := a.AddBlock(-1, nil, nil)
	assertErrorCode(t, err, ErrNegativeBlockID)

	if err := a.AddBlock(0, nil, nil); err != nil {
		t.Fatal(err)
	}
	// 0 已存在且为负不可能；重复优先于封口，封口前无法触发“已封口”，
	// 故“重复 > 已封口”的优先级在封口后验证。
	err = a.AddBlock(0, nil, nil)
	assertErrorCode(t, err, ErrDuplicateBlockID)

	if err := a.Seal(); err != nil {
		t.Fatal(err)
	}
	// 封口后：负数仍最先报；其次已存在；最后才是封口拒绝。
	assertErrorCode(t, a.AddBlock(-3, nil, nil), ErrNegativeBlockID)
	assertErrorCode(t, a.AddBlock(0, nil, nil), ErrDuplicateBlockID)
	assertErrorCode(t, a.AddBlock(7, nil, nil), ErrAlreadySealed)

	// 被拒绝的操作不改变状态：仍只有 B0。
	snaps, _ := a.Blocks()
	if len(snaps) != 1 || snaps[0].ID != 0 {
		t.Fatalf("拒绝后状态被污染: %+v", snaps)
	}
	t.Log("判定：添加校验顺序 负数→已存在→已封口；拒绝后块集合不变")
}

func TestSealValidationPriorityAndFirstMissingRef(t *testing.T) {
	// 重复封口
	sealed := NewAnalyzer()
	_ = sealed.AddBlock(0, nil, nil)
	if err := sealed.Seal(); err != nil {
		t.Fatal(err)
	}
	assertErrorCode(t, sealed.Seal(), ErrSealedTwice)

	// 没有任何块
	empty := NewAnalyzer()
	assertErrorCode(t, empty.Seal(), ErrNoBlocks)

	// 后继不存在：按引用方块编号升序、块内后继出现序报第一处。
	a := NewAnalyzer()
	mustAdd := func(id int, succ ...int) {
		if err := a.AddBlock(id, nil, succ); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd(2, 8)    // B2 引用缺失 8
	mustAdd(1, 3, 2) // B1 引用缺失 3（出现序先于已存在的 2）
	mustAdd(0, 4, 1) // B0 引用缺失 4
	err := a.Seal()
	assertErrorCode(t, err, ErrMissingSuccessor)
	ae := err.(*AnalysisError)
	if want := "block 0 references missing successor 4"; ae.Detail != want {
		t.Fatalf("首个缺失后继=%q, want %q", ae.Detail, want)
	}

	// 封口失败不污染状态：已有 3 块且未封口，补齐后可成功封口。
	if a.Sealed() {
		t.Fatal("封口失败不应置位 sealed")
	}
	snapsBefore, err := a.Blocks()
	if codeOf(err) != ErrNotSealed {
		t.Fatalf("封口失败后查询应报 not_sealed, got %v", err)
	}
	if snapsBefore != nil {
		t.Fatal("未封口查询必须整体拒绝且不返回数据")
	}
	_ = a.AddBlock(3, nil, nil)
	_ = a.AddBlock(4, nil, nil)
	_ = a.AddBlock(8, nil, nil)
	if err := a.Seal(); err != nil {
		t.Fatalf("补齐后继后封口应成功: %v", err)
	}
	t.Log("判定：缺失后继取 B0 的首个引用 4；失败后可继续添加并再次封口")
}

func TestQueryValidation(t *testing.T) {
	a := NewAnalyzer()
	_ = a.AddBlock(0, nil, nil)

	// 尚未封口：封口状态优先于块是否存在。
	if _, err := a.Block(99); codeOf(err) != ErrNotSealed {
		t.Fatalf("未封口查不存在块, code=%v want not_sealed", codeOf(err))
	}
	if _, err := a.Blocks(); codeOf(err) != ErrNotSealed {
		t.Fatalf("未封口 Blocks code=%v", codeOf(err))
	}
	if _, err := a.EntryID(); codeOf(err) != ErrNotSealed {
		t.Fatalf("未封口 EntryID code=%v", codeOf(err))
	}
	if _, err := a.EntryLiveIn(); codeOf(err) != ErrNotSealed {
		t.Fatalf("未封口 EntryLiveIn code=%v", codeOf(err))
	}

	if err := a.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Block(42); codeOf(err) != ErrNoSuchBlock {
		t.Fatalf("封口后查不存在块 code=%v want no_such_block", codeOf(err))
	}
	if s, err := a.Block(0); err != nil || s.ID != 0 {
		t.Fatalf("封口后查 B0 失败: %+v %v", s, err)
	}
	t.Log("判定：查询先查封口状态，再查块存在性；错误可区分且整体拒绝")
}

func TestDeterministicReplay(t *testing.T) {
	order := []int{3, 1, 2}
	spec := map[int]blockSpec{
		3: {succ: []int{1, 2}},
		1: {insts: []Instruction{inst(nil, []string{"x"})}, succ: []int{2}},
		2: {insts: []Instruction{inst([]string{"x", "a"}, []string{"a"})}, succ: []int{3}},
	}
	run := func() []BlockSnapshot {
		a := buildAnalyzer(t, order, spec)
		return assertAgainstNaive(t, a, order, spec)
	}
	first := run()
	for i := 0; i < 5; i++ {
		if !reflect.DeepEqual(run(), first) {
			t.Fatal("相同录入序列重放结果不一致")
		}
	}
	// 块按编号升序、变量按字典序。
	if got := []int{first[0].ID, first[1].ID, first[2].ID}; !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("Blocks 顺序=%v, want 升序 [1 2 3]", got)
	}
	for _, s := range first {
		if !sortedStrings(s.UE) || !sortedStrings(s.Def) || !sortedStrings(s.LiveIn) || !sortedStrings(s.LiveOut) {
			t.Fatalf("B%d 存在未排序输出: %+v", s.ID, s)
		}
	}
	t.Logf("判定：6 次重放完全一致，输出顺序确定：%+v", first)
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

// 并发录入 + 并发查询/封口：串行等价（-race 下无数据竞争），
// 且只有一次封口成功；封口后查询结果恒定。
func TestConcurrentAccess(t *testing.T) {
	const n = 50
	a := NewAnalyzer()
	var wg sync.WaitGroup

	// 多协程并发录入不同块，每块指向下一块（含前向引用）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := worker; i < n; i += 4 {
				succ := []int{(i + 1) % n}
				_ = a.AddBlock(i, []Instruction{inst([]string{fmt.Sprintf("v%d", i)}, nil)}, succ)
			}
		}(w)
	}

	// 录入期间并发封口：可能因后继缺失失败，也可能在最后成功，
	// 但成功至多一次，且成功发生在全部块到齐之后。
	var sealWins int32
	var sealMu sync.Mutex
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := a.Seal()
			if err == nil {
				sealMu.Lock()
				sealWins++
				sealMu.Unlock()
				return
			}
		}
	}()

	wg.Wait() // 等待所有 AddBlock 完成
	// 所有块到齐后兜底封口（若竞态封口器尚未成功）。
	if err := a.Seal(); err != nil && codeOf(err) != ErrSealedTwice {
		t.Fatalf("全部录入后封口异常: %v", err)
	}
	close(stop)

	if !a.Sealed() {
		t.Fatal("最终应已封口")
	}
	if sealWins > 1 {
		t.Fatalf("成功封口次数=%d, 至多 1", sealWins)
	}

	// 封口后并发查询，结果必须恒定且与朴素迭代一致。
	spec := map[int]blockSpec{}
	for i := 0; i < n; i++ {
		spec[i] = blockSpec{
			insts: []Instruction{inst([]string{fmt.Sprintf("v%d", i)}, nil)},
			succ:  []int{(i + 1) % n},
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	want := naiveReference(order, spec)

	var qwg sync.WaitGroup
	for w := 0; w < 8; w++ {
		qwg.Add(1)
		go func() {
			defer qwg.Done()
			for i := 0; i < n; i++ {
				s, err := a.Block(i)
				if err != nil {
					t.Errorf("Block(%d): %v", i, err)
					return
				}
				rb := want[i]
				if !reflect.DeepEqual(s.LiveIn, boolKeys(rb.in)) || !reflect.DeepEqual(s.LiveOut, boolKeys(rb.out)) {
					t.Errorf("封口后查询结果漂移: B%d in%v/out%v want in%v/out%v",
						i, s.LiveIn, s.LiveOut, boolKeys(rb.in), boolKeys(rb.out))
					return
				}
			}
		}()
	}
	qwg.Wait()
	t.Log("判定：并发录入/封口/查询无竞态；封口仅成功一次；封口后 8 协程并发查询结果恒定且等于最小不动点")
}
