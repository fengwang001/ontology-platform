package iblt

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// buildSketch 把 keys 逐个 Add 进 m 格草图。
func buildSketch(t *testing.T, m int, keys []uint64) *Sketch {
	t.Helper()
	s, err := New(m)
	if err != nil {
		t.Fatalf("New(%d): %v", m, err)
	}
	for _, k := range keys {
		s.Add(k)
	}
	return s
}

// reconcile 走完整对账流程：各自写入、相减、解码。
func reconcile(t *testing.T, m int, aKeys, bKeys []uint64, limit int) ([]uint64, []uint64, error) {
	t.Helper()
	a := buildSketch(t, m, aKeys)
	b := buildSketch(t, m, bKeys)
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatalf("Subtract: %v", err)
	}
	return diff.Decode(limit)
}

// naiveDiff 朴素计算对称差的两侧。
func naiveDiff(aKeys, bKeys []uint64) (onlyA, onlyB []uint64) {
	inB := make(map[uint64]bool, len(bKeys))
	for _, k := range bKeys {
		inB[k] = true
	}
	inA := make(map[uint64]bool, len(aKeys))
	for _, k := range aKeys {
		inA[k] = true
		if !inB[k] {
			onlyA = append(onlyA, k)
		}
	}
	for _, k := range bKeys {
		if !inA[k] {
			onlyB = append(onlyB, k)
		}
	}
	sort.Slice(onlyA, func(i, j int) bool { return onlyA[i] < onlyA[j] })
	sort.Slice(onlyB, func(i, j int) bool { return onlyB[i] < onlyB[j] })
	return
}

func TestNewInvalidSize(t *testing.T) {
	for _, m := range []int{0, -3, 1, 2, 4, 5, 7, 100} {
		if _, err := New(m); !errors.Is(err, ErrInvalidSize) {
			t.Fatalf("New(%d): 期望 ErrInvalidSize，得到 %v", m, err)
		}
		t.Logf("New(%d) 被拒绝: 判定依据 m 不是 3 的正整数倍", m)
	}
	for _, m := range []int{3, 6, 300} {
		if _, err := New(m); err != nil {
			t.Fatalf("New(%d): 期望成功，得到 %v", m, err)
		}
	}
}

func TestSubtractSizeMismatch(t *testing.T) {
	a := buildSketch(t, 300, []uint64{1, 2, 3})
	b := buildSketch(t, 303, []uint64{1, 2, 3})
	before := a.snapshot()
	diff, err := Subtract(a, b)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("期望 ErrSizeMismatch，得到 %v", err)
	}
	if diff != nil {
		t.Fatalf("被拒绝的 Subtract 不得产生结果")
	}
	if !reflect.DeepEqual(before, a.snapshot()) {
		t.Fatalf("被拒绝的 Subtract 改变了草图")
	}
	t.Logf("Subtract(m=300, m=303) 被拒绝: 判定依据格子数不同, err=%v", err)
}

func TestDecodeNegativeLimit(t *testing.T) {
	s := buildSketch(t, 300, []uint64{1, 2, 3})
	before := s.snapshot()
	if _, _, err := s.Decode(-1); !errors.Is(err, ErrNegativeLimit) {
		t.Fatalf("期望 ErrNegativeLimit，得到 %v", err)
	}
	if !reflect.DeepEqual(before, s.snapshot()) {
		t.Fatalf("被拒绝的 Decode 改变了草图")
	}
	t.Logf("Decode(-1) 被拒绝: 判定依据 limit 为负，优先于其他错误")
}

func TestKeyZeroRecoverable(t *testing.T) {
	aKeys := []uint64{0, 11, 22, 33}
	bKeys := []uint64{11, 22, 44}
	onlyA, onlyB, err := reconcile(t, 300, aKeys, bKeys, 10)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	wantA, wantB := naiveDiff(aKeys, bKeys)
	if !reflect.DeepEqual(onlyA, wantA) || !reflect.DeepEqual(onlyB, wantB) {
		t.Fatalf("got (%v, %v), want (%v, %v)", onlyA, onlyB, wantA, wantB)
	}
	if onlyA[0] != 0 {
		t.Fatalf("键 0 未被恢复到仅 a 侧: %v", onlyA)
	}
	t.Logf("输入 a=%v b=%v, 输出 onlyA=%v onlyB=%v, 判定依据: 键 0 正常参与 mix/校验并可剥离",
		aKeys, bKeys, onlyA, onlyB)
}

// TestImpureCountOneCell 用暴力搜索找三个键，使某格计数为 +1+1-1=1，
// 但键异或与校验异或来自多个键，因而不是纯格子，解码必须失败。
func TestImpureCountOneCell(t *testing.T) {
	// m把三个键都压进同一个格子：取 m=3，则每个键的三个位置都是 0,1,2。
	var found bool
	var a, b, c uint64
	for x := uint64(1); x < 64 && !found; x++ {
		for y := x + 1; y < 64 && !found; y++ {
			for z := y + 1; z < 64 && !found; z++ {
				checkXor := checksum(x) ^ checksum(y) ^ checksum(z)
				// 计数为 1 时纯格子要求 g(keyXor)==checkXor；找不满足者。
				if checksum(x^y^z) != checkXor {
					a, b, c = x, y, z
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("暴力搜索未找到构造")
	}
	s, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(a)
	s.Add(b)
	s.Remove(c)
	for i, cell := range s.snapshot() {
		if cell.count != 1 {
			t.Fatalf("格子 %d 计数=%d，期望 1", i, cell.count)
		}
		if checksum(cell.keyXor) == cell.checkXor {
			t.Fatalf("格子 %d 意外成为纯格子", i)
		}
	}
	before := s.snapshot()
	if _, _, err := s.Decode(10); !errors.Is(err, ErrNotDecodable) {
		t.Fatalf("期望 ErrNotDecodable，得到 %v", err)
	}
	if !reflect.DeepEqual(before, s.snapshot()) {
		t.Fatalf("失败的 Decode 改变了草图")
	}
	t.Logf("输入 Add(%d),Add(%d),Remove(%d) 于 m=3: 每格计数=+1+1-1=1，"+
		"但 g(keyXor)!=checkXor，判定依据: 计数为 1 却含多个键，不是纯格子，不可解码", a, b, c)
}

// TestCountZeroNonZeroNotPure 计数为 0 而分量非零的格子不是纯格子。
func TestCountZeroNonZeroNotPure(t *testing.T) {
	s, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	// 计数 +1+1-1-1=0，键异或=2^3!=0，校验异或=g(2)^g(3)!=0。
	s.Add(1)
	s.Add(2)
	s.Remove(1)
	s.Remove(3)
	for i, cell := range s.snapshot() {
		if cell.count != 0 || cell.keyXor == 0 || cell.checkXor == 0 {
			t.Fatalf("格子 %d = %+v，期望计数 0 且分量非零", i, cell)
		}
	}
	if _, _, err := s.Decode(10); !errors.Is(err, ErrNotDecodable) {
		t.Fatalf("期望 ErrNotDecodable，得到 %v", err)
	}
	t.Logf("输入 Add(1),Add(2),Remove(1),Remove(3) 于 m=3: 计数为 0 而分量非零，" +
		"判定依据: 纯格子要求计数为 ±1，该格不纯，不可解码")
}

// TestNegativeCountGoesToB 计数为 -1 的键归仅 b 侧，撤销用 Add 的效果。
func TestNegativeCountGoesToB(t *testing.T) {
	a := buildSketch(t, 300, []uint64{7, 8, 9})
	b := buildSketch(t, 300, []uint64{7, 8, 9, 42, 1000})
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}
	onlyA, onlyB, err := diff.Decode(10)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(onlyA) != 0 {
		t.Fatalf("onlyA=%v，期望空", onlyA)
	}
	if !reflect.DeepEqual(onlyB, []uint64{42, 1000}) {
		t.Fatalf("onlyB=%v，期望 [42 1000]", onlyB)
	}
	t.Logf("输入 a={7,8,9} b={7,8,9,42,1000}: 相减后 42 与 1000 计数为 -1，" +
		"判定依据: 计数 -1 归仅 b 侧，撤销时用 Add 的效果（计数 +1），输出 onlyB=[42 1000]")
}

// TestLimitBoundary limit 恰等于差集大小成功，小 1 报超出上限。
func TestLimitBoundary(t *testing.T) {
	aKeys := []uint64{10, 20, 30, 40}
	bKeys := []uint64{50, 60, 70, 80}
	const diffSize = 8

	onlyA, onlyB, err := reconcile(t, 300, aKeys, bKeys, diffSize)
	if err != nil {
		t.Fatalf("limit=%d 应成功: %v", diffSize, err)
	}
	wantA, wantB := naiveDiff(aKeys, bKeys)
	if !reflect.DeepEqual(onlyA, wantA) || !reflect.DeepEqual(onlyB, wantB) {
		t.Fatalf("got (%v, %v), want (%v, %v)", onlyA, onlyB, wantA, wantB)
	}
	t.Logf("limit=%d 等于差集大小: 成功, onlyA=%v onlyB=%v", diffSize, onlyA, onlyB)

	a := buildSketch(t, 300, aKeys)
	b := buildSketch(t, 300, bKeys)
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}
	before := diff.snapshot()
	if _, _, err := diff.Decode(diffSize - 1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("limit=%d 期望 ErrLimitExceeded，得到 %v", diffSize-1, err)
	}
	if !reflect.DeepEqual(before, diff.snapshot()) {
		t.Fatalf("超限的 Decode 改变了草图")
	}
	t.Logf("limit=%d 小于差集大小 %d: 即将记入第 %d 个键时报超出上限, err=%v",
		diffSize-1, diffSize, diffSize, ErrLimitExceeded)
}

// TestTooSmallSketchNotDecodable 过小草图报不可解码且不改状态。
func TestTooSmallSketchNotDecodable(t *testing.T) {
	// m=3 只有 3 格，4 个键几乎必然互相污染。
	aKeys := []uint64{1, 2, 3, 4}
	s := buildSketch(t, 3, aKeys)
	before := s.snapshot()
	_, _, err := s.Decode(100)
	if !errors.Is(err, ErrNotDecodable) {
		t.Fatalf("期望 ErrNotDecodable，得到 %v", err)
	}
	if !reflect.DeepEqual(before, s.snapshot()) {
		t.Fatalf("失败的 Decode 改变了草图")
	}
	t.Logf("输入 keys=%v 于 m=3: 报不可解码且草图状态不变, err=%v", aKeys, err)
}

// TestSelfSubtract 同一草图自己减自己合法且得到全零草图。
func TestSelfSubtract(t *testing.T) {
	s := buildSketch(t, 300, []uint64{5, 6, 7, 8})
	diff, err := Subtract(s, s)
	if err != nil {
		t.Fatalf("Subtract(s, s): %v", err)
	}
	for i, c := range diff.snapshot() {
		if c.count != 0 || c.keyXor != 0 || c.checkXor != 0 {
			t.Fatalf("格子 %d 非零: %+v", i, c)
		}
	}
	onlyA, onlyB, err := diff.Decode(0)
	if err != nil {
		t.Fatalf("全零草图 Decode(0) 应成功: %v", err)
	}
	if len(onlyA) != 0 || len(onlyB) != 0 {
		t.Fatalf("全零草图应解出空列表: %v %v", onlyA, onlyB)
	}
	t.Logf("Subtract(s, s) 得到全零草图，Decode(0) 成功返回空列表")
}

// TestOrderIndependent 同一批 Add/Remove 以任意顺序得到逐格相同的草图。
func TestOrderIndependent(t *testing.T) {
	type op struct {
		key   uint64
		delta bool // true=Add false=Remove
	}
	ops := []op{{1, true}, {2, true}, {3, false}, {4, true}, {2, false}, {5, true}}
	ref, err := New(300)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if o.delta {
			ref.Add(o.key)
		} else {
			ref.Remove(o.key)
		}
	}
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(ops))
		s, err := New(300)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range perm {
			if ops[i].delta {
				s.Add(ops[i].key)
			} else {
				s.Remove(ops[i].key)
			}
		}
		if !reflect.DeepEqual(ref.snapshot(), s.snapshot()) {
			t.Fatalf("次序 %v 得到不同草图", perm)
		}
	}
	t.Logf("20 组随机次序的 Add/Remove 均得到逐格相同的草图")
}

// TestAddThenRemoveRestores 对未写入过的键先 Add 再 Remove 恢复为原状。
func TestAddThenRemoveRestores(t *testing.T) {
	s := buildSketch(t, 300, []uint64{9, 99})
	before := s.snapshot()
	s.Add(12345)
	s.Remove(12345)
	if !reflect.DeepEqual(before, s.snapshot()) {
		t.Fatalf("Add 后 Remove 未恢复原状")
	}
	t.Logf("对未写入过的键 12345 先 Add 再 Remove，草图逐格恢复原状")
}

// TestDecodeNoAlias Decode 返回的列表不得与内部状态别名。
func TestDecodeNoAlias(t *testing.T) {
	aKeys := []uint64{1, 2, 3}
	bKeys := []uint64{2, 3, 4}
	a := buildSketch(t, 300, aKeys)
	b := buildSketch(t, 300, bKeys)
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}
	onlyA1, onlyB1, err := diff.Decode(10)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改返回的列表，再次 Decode 结果必须不变。
	onlyA1[0] = 0xFFFFFFFFFFFFFFFF
	onlyB1[0] = 0xFFFFFFFFFFFFFFFF
	onlyA2, onlyB2, err := diff.Decode(10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(onlyA2, []uint64{1}) || !reflect.DeepEqual(onlyB2, []uint64{4}) {
		t.Fatalf("返回列表与内部状态别名: %v %v", onlyA2, onlyB2)
	}
	t.Logf("篡改第一次 Decode 返回的列表后，再次 Decode 结果不变: onlyA=%v onlyB=%v", onlyA2, onlyB2)
}

// TestConcurrent 并发调用 Add/Remove/Subtract/Decode，结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	const workers = 8
	const keysPerWorker = 50
	a, err := New(300)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(300)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint64(w * keysPerWorker)
			for i := uint64(0); i < keysPerWorker; i++ {
				a.Add(base + i)
				if i%3 != 0 {
					b.Add(base + i)
				}
			}
			for i := uint64(0); i < keysPerWorker; i += 7 {
				a.Remove(base + i) // 对未写入 b 的键也并发 Remove
			}
			if _, err := Subtract(a, a); err != nil { // 自减不得死锁
				t.Error(err)
			}
			if _, err := Subtract(a, b); err != nil {
				t.Error(err)
			}
			// 高密度草图 Decode 返回不可解码属正常，只要不 panic、不数据竞争即可。
			_, _, _ = a.Decode(1000)
		}(w)
	}
	wg.Wait()

	// 串行参照：同样的操作按固定顺序重放。
	refA, err := New(300)
	if err != nil {
		t.Fatal(err)
	}
	for w := 0; w < workers; w++ {
		base := uint64(w * keysPerWorker)
		for i := uint64(0); i < keysPerWorker; i++ {
			refA.Add(base + i)
		}
		for i := uint64(0); i < keysPerWorker; i += 7 {
			refA.Remove(base + i)
		}
	}
	if !reflect.DeepEqual(refA.snapshot(), a.snapshot()) {
		t.Fatalf("并发 Add/Remove 的结果与串行参照不一致")
	}
	t.Logf("%d 个 goroutine 并发 Add/Remove/Subtract/Decode，最终草图与串行参照逐格一致", workers)
}

// TestRandomReconcile 在 m=300 的草图上对差异不超过 8 的 2000 组随机集合对
// 与朴素集合差对拍：解码成功必须一致，失败次数打印在日志中。
func TestRandomReconcile(t *testing.T) {
	const (
		m       = 300
		trials  = 2000
		maxDiff = 8
	)
	rng := rand.New(rand.NewSource(20261001))
	var failDecode, mismatch int
	for trial := 0; trial < trials; trial++ {
		// 公共部分 + 各自独有部分，保证对称差大小 <= maxDiff。
		common := make(map[uint64]bool)
		for len(common) < 20+rng.Intn(30) {
			common[rng.Uint64()] = true
		}
		aOnly := make(map[uint64]bool)
		for len(aOnly) < rng.Intn(maxDiff/2+1) {
			k := rng.Uint64()
			if !common[k] {
				aOnly[k] = true
			}
		}
		bOnly := make(map[uint64]bool)
		for len(bOnly) < maxDiff-len(aOnly) && len(bOnly) < rng.Intn(maxDiff/2+1) {
			k := rng.Uint64()
			if !common[k] && !aOnly[k] {
				bOnly[k] = true
			}
		}
		var aKeys, bKeys []uint64
		for k := range common {
			aKeys = append(aKeys, k)
			bKeys = append(bKeys, k)
		}
		for k := range aOnly {
			aKeys = append(aKeys, k)
		}
		for k := range bOnly {
			bKeys = append(bKeys, k)
		}

		a := buildSketch(t, m, aKeys)
		b := buildSketch(t, m, bKeys)
		diff, err := Subtract(a, b)
		if err != nil {
			t.Fatal(err)
		}
		onlyA, onlyB, err := diff.Decode(maxDiff)
		wantA, wantB := naiveDiff(aKeys, bKeys)
		if err != nil {
			if !errors.Is(err, ErrNotDecodable) && !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("trial %d: 意外错误 %v", trial, err)
			}
			failDecode++
			if failDecode <= 5 {
				t.Logf("trial %d 解码失败: 差集大小=%d, err=%v; 判定依据: 剥离提前终止且格子有残留",
					trial, len(wantA)+len(wantB), err)
			}
			continue
		}
		if !reflect.DeepEqual(onlyA, wantA) || !reflect.DeepEqual(onlyB, wantB) {
			mismatch++
			t.Errorf("trial %d 对拍不一致: got (%v, %v), want (%v, %v)",
				trial, onlyA, onlyB, wantA, wantB)
		}
	}
	t.Logf("m=%d, %d 组随机集合对（差异<=%d）: 解码失败 %d 次，成功 %d 次，对拍不一致 %d 次",
		m, trials, maxDiff, failDecode, trials-failDecode, mismatch)
	if failDecode == trials {
		t.Fatalf("全部 %d 组都解码失败，草图或解码实现可能有误", trials)
	}
}
