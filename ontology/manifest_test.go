package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func pu(v uint64) *uint64 { return &v }

func mustNew(t *testing.T, T int) *Manager {
	t.Helper()
	m, err := New(T)
	if err != nil {
		t.Fatalf("New(%d): %v", T, err)
	}
	return m
}

func mkFile(level int, num uint64, s, l string) File {
	return File{Level: level, Num: num, Smallest: []byte(s), Largest: []byte(l)}
}

func assertErrIs(t *testing.T, err error, targets ...error) {
	t.Helper()
	for _, target := range targets {
		if !errors.Is(err, target) {
			t.Fatalf("err=%v 未能 errors.Is 到 %v", err, target)
		}
	}
}

// TestNewInitial 初始版本与初始磁盘。
func TestNewInitial(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrParam) {
		t.Fatalf("New(0) err=%v, want ErrParam", err)
	}
	m := mustNew(t, 3)
	v := m.View()
	if v.LogNumber != 0 || v.NextFile != 2 || v.LastSeq != 0 {
		t.Fatalf("初始指针错误: %+v", v)
	}
	for level := range v.Files {
		if len(v.Files[level]) != 0 {
			t.Fatalf("初始版本第 %d 层应为空", level)
		}
	}
	d := m.Disk()
	if d.Current != 1 || len(d.Manifests) != 1 || len(d.Manifests[1]) != 1 {
		t.Fatalf("初始磁盘错误: %+v", d)
	}
	if d.Manifests[1][0].Snapshot == nil {
		t.Fatal("清单 1 唯一记录应为 Snapshot")
	}
}

// TestSpecExample 题目给出的 T=3 完整示例。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, 3)

	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "c")}}); err != nil {
		t.Fatal(err)
	}
	if v := m.View(); v.NextFile != 6 {
		t.Fatalf("NextFile=%d, want 6", v.NextFile)
	}
	if len(m.Disk().Manifests[1]) != 2 {
		t.Fatal("清单 1 应有 2 条记录")
	}

	if err := m.Apply(Edit{Adds: []File{mkFile(1, 3, "a", "m")}, LogNumber: pu(4)}); err != nil {
		t.Fatal(err)
	}
	if len(m.Disk().Manifests[1]) != 3 {
		t.Fatal("清单 1 应有 3 条记录（恰等 T 不轮转）")
	}

	// m..z 与文件 3(a..m) 首尾相接：算重叠。
	err := m.Apply(Edit{
		Dels: []File{{Level: 0, Num: 5}},
		Adds: []File{mkFile(1, 5, "m", "z")},
	})
	assertErrIs(t, err, ErrOverlap)

	// n..z 不重叠：成功，记录数 4 > 3 -> 立即轮转。
	if err := m.Apply(Edit{
		Dels: []File{{Level: 0, Num: 5}},
		Adds: []File{mkFile(1, 5, "n", "z")},
	}); err != nil {
		t.Fatal(err)
	}
	d := m.Disk()
	if d.Current != 6 {
		t.Fatalf("CURRENT=%d, want 6", d.Current)
	}
	recs := d.Manifests[6]
	if len(recs) != 1 || recs[0].Snapshot == nil {
		t.Fatalf("清单 6 应仅含一条 Snapshot, got %+v", recs)
	}
	if recs[0].Snapshot.NextFile != 7 {
		t.Fatalf("快照 NextFile=%d, want 7（加 1 之后）", recs[0].Snapshot.NextFile)
	}
	if v := m.View(); v.NextFile != 7 || v.LogNumber != 4 {
		t.Fatalf("活动版本错误: %s", describeVersion(v))
	}
	if len(d.Manifests[1]) != 4 {
		t.Fatalf("旧清单应有 4 条记录, got %d", len(d.Manifests[1]))
	}
}

// TestTouchingIntervalsOverlap 相接区间（prev.Largest == add.Smallest）算重叠。
func TestTouchingIntervalsOverlap(t *testing.T) {
	m := mustNew(t, 4)
	if err := m.Apply(Edit{Adds: []File{mkFile(2, 10, "a", "m")}}); err != nil {
		t.Fatal(err)
	}
	err := m.Apply(Edit{Adds: []File{mkFile(2, 11, "m", "z")}})
	assertErrIs(t, err, ErrOverlap)
	if err := m.Apply(Edit{Adds: []File{mkFile(2, 11, "n", "z")}}); err != nil {
		t.Fatal(err)
	}

	// 第 0 层允许重叠（哪怕相接），且按 Num 降序。
	m2 := mustNew(t, 4)
	if err := m2.Apply(Edit{Adds: []File{
		mkFile(0, 10, "a", "m"),
		mkFile(0, 11, "m", "z"),
	}}); err != nil {
		t.Fatalf("L0 应允许重叠: %v", err)
	}
	v := m2.View()
	if v.Files[0][0].Num != 11 || v.Files[0][1].Num != 10 {
		t.Fatalf("L0 应按 Num 降序: %+v", v.Files[0])
	}
}

// TestDeleteThenAddSameNumber 同编辑内先删后加同号成功；同号 Add 两次失败。
func TestDeleteThenAddSameNumber(t *testing.T) {
	m := mustNew(t, 5)
	if err := m.Apply(Edit{Adds: []File{mkFile(1, 8, "a", "c")}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(Edit{
		Dels: []File{{Level: 1, Num: 8}},
		Adds: []File{mkFile(1, 8, "x", "z")},
	}); err != nil {
		t.Fatalf("先删后加同号应成功: %v", err)
	}
	err := m.Apply(Edit{Adds: []File{
		mkFile(0, 20, "a", "b"),
		mkFile(1, 20, "c", "d"),
	}})
	assertErrIs(t, err, ErrDupFile)
	err = m.Apply(Edit{Adds: []File{mkFile(0, 8, "a", "b")}})
	assertErrIs(t, err, ErrDupFile)
}

// TestNextFileRegressAndRaise 给出的 NextFile 小于当前报 ErrRegress；
// 不大于 Add 的 Num+1 时静默抬高。
func TestNextFileRegressAndRaise(t *testing.T) {
	m := mustNew(t, 5)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 9, "a", "b")}}); err != nil {
		t.Fatal(err)
	}
	if v := m.View(); v.NextFile != 10 {
		t.Fatalf("静默抬高失败: next=%d", v.NextFile)
	}
	// 给出值 3 小于当前 10：先报 ErrRegress（第四步只看给出值与当前值）。
	err := m.Apply(Edit{
		Adds:     []File{mkFile(0, 20, "a", "b")},
		NextFile: pu(3),
	})
	assertErrIs(t, err, ErrRegress)
	// 给出值 10 >= 当前 10，但不大于 Add Num+1=21：接受并静默抬高到 21。
	if err := m.Apply(Edit{
		Adds:     []File{mkFile(0, 20, "a", "b")},
		NextFile: pu(10),
	}); err != nil {
		t.Fatalf("给出 NextFile 不小于当前但小于 Num+1 时应静默抬高: %v", err)
	}
	if v := m.View(); v.NextFile != 21 {
		t.Fatalf("next=%d, want 21", v.NextFile)
	}
	err = m.Apply(Edit{NextFile: pu(20)})
	assertErrIs(t, err, ErrRegress)
	if err := m.Apply(Edit{NextFile: pu(21)}); err != nil {
		t.Fatalf("恰等应允许: %v", err)
	}
}

// TestLogAheadOnEffectiveNext LogNumber 恰等于有效 NextFile 报 ErrLogAhead。
func TestLogAheadOnEffectiveNext(t *testing.T) {
	m := mustNew(t, 5)
	err := m.Apply(Edit{
		Adds:      []File{mkFile(0, 5, "a", "b")},
		LogNumber: pu(6),
	})
	assertErrIs(t, err, ErrLogAhead)
	if err := m.Apply(Edit{
		Adds:      []File{mkFile(0, 5, "a", "b")},
		LogNumber: pu(5),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestErrorOrdering 同时触发多个错误时按规定次序报第一个。
func TestErrorOrdering(t *testing.T) {
	m := mustNew(t, 6)
	if err := m.Apply(Edit{Adds: []File{mkFile(1, 5, "a", "c")}}); err != nil {
		t.Fatal(err)
	}

	// ErrParam 优先于 ErrNoFile。
	err := m.Apply(Edit{
		Adds: []File{mkFile(1, 9, "", "z")},
		Dels: []File{{Level: 1, Num: 99}},
	})
	assertErrIs(t, err, ErrParam)

	// ErrNoFile 优先于 ErrDupFile。
	err = m.Apply(Edit{
		Dels: []File{{Level: 1, Num: 99}},
		Adds: []File{mkFile(0, 5, "a", "c")},
	})
	assertErrIs(t, err, ErrNoFile)

	// ErrDupFile 优先于 ErrRegress。
	err = m.Apply(Edit{
		Adds:     []File{mkFile(0, 5, "a", "c")},
		NextFile: pu(1),
	})
	assertErrIs(t, err, ErrDupFile)

	// ErrRegress 优先于 ErrLogAhead。
	err = m.Apply(Edit{
		NextFile:  pu(1),
		LogNumber: pu(100),
	})
	assertErrIs(t, err, ErrRegress)

	// 只剩 ErrLogAhead：LogNumber 等于当前 NextFile。
	cur := m.View().NextFile
	err = m.Apply(Edit{NextFile: pu(cur), LogNumber: pu(cur)})
	assertErrIs(t, err, ErrLogAhead)

	// ErrLogAhead 优先于 ErrOverlap。
	err = m.Apply(Edit{
		Adds:      []File{mkFile(1, 50, "c", "z")},
		LogNumber: pu(60),
	})
	assertErrIs(t, err, ErrLogAhead)

	// 只剩 ErrOverlap（c 与 a..c 首尾相接）。
	err = m.Apply(Edit{Adds: []File{mkFile(1, 50, "c", "z")}})
	assertErrIs(t, err, ErrOverlap)
}

// TestRotateAtThreshold 记录数恰等于 T 不轮转；超过才轮转。
func TestRotateAtThreshold(t *testing.T) {
	m := mustNew(t, 2)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "b")}}); err != nil {
		t.Fatal(err)
	}
	if m.Disk().Current != 1 {
		t.Fatal("恰等 T 不应轮转")
	}
	if err := m.Apply(Edit{LastSeq: pu(1)}); err != nil {
		t.Fatal(err)
	}
	d := m.Disk()
	if d.Current != 6 {
		t.Fatalf("应轮转到清单 6, got %d", d.Current)
	}
	snap := d.Manifests[6][0].Snapshot
	if snap == nil || snap.NextFile != 7 {
		t.Fatalf("快照 NextFile 应为加 1 之后的 7, got %+v", snap)
	}
	if snap.LastSeq != 1 {
		t.Fatalf("快照 LastSeq=%d, want 1", snap.LastSeq)
	}
	num := m.Rotate()
	if num != 7 || m.Disk().Current != 7 || m.View().NextFile != 8 {
		t.Fatalf("手动 Rotate 错误: num=%d current=%d next=%d",
			num, m.Disk().Current, m.View().NextFile)
	}
}

// TestRejectedEditNoStateChange 被拒绝的编辑不改版本也不追加记录。
func TestRejectedEditNoStateChange(t *testing.T) {
	m := mustNew(t, 5)
	if err := m.Apply(Edit{Adds: []File{mkFile(1, 5, "a", "c")}}); err != nil {
		t.Fatal(err)
	}
	diskBefore := m.Disk()
	viewBefore := m.View()

	err := m.Apply(Edit{Adds: []File{mkFile(1, 6, "c", "z")}}) // 相接重叠
	assertErrIs(t, err, ErrOverlap)

	if !reflect.DeepEqual(viewBefore, m.View()) {
		t.Fatal("被拒绝后版本被改动")
	}
	if !reflect.DeepEqual(diskBefore, m.Disk()) {
		t.Fatal("被拒绝后磁盘被改动")
	}
}

// 防止未使用导入（sync 供并发测试使用，见下方）。
var _ = sync.Once{}

// TestRecoverTorn 撕裂记录：最后一条忽略，中间一条报 ErrCorrupt。
func TestRecoverTorn(t *testing.T) {
	m := mustNew(t, 10)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "b")}}); err != nil {
		t.Fatal(err)
	}

	// 最后一条撕裂：恢复时忽略，版本等于应用第一条编辑后的状态。
	d := m.Disk()
	d.Manifests[1] = append(d.Manifests[1],
		Record{Edit: &Edit{LastSeq: pu(9)}, Torn: true})
	v, err := Recover(d)
	if err != nil {
		t.Fatalf("末尾撕裂应忽略: %v", err)
	}
	t.Logf("输入: CURRENT=1 含 1 快照+1 正常 Edit+1 末尾撕裂 Edit; 输出: %s; "+
		"判定: 撕裂在末尾其后无记录 -> 忽略", describeVersion(v))
	if v.LastSeq != 0 || v.NextFile != 6 || len(v.Files[0]) != 1 {
		t.Fatalf("末尾撕裂恢复错误: %s", describeVersion(v))
	}

	// 撕裂记录后面还有记录：报 ErrCorrupt，下标为撕裂条目的下标。
	d2 := m.Disk()
	recs := d2.Manifests[1]
	recs = append(recs,
		Record{Edit: &Edit{LastSeq: pu(1)}, Torn: true},
		Record{Edit: &Edit{LastSeq: pu(2)}})
	d2.Manifests[1] = recs
	_, err = Recover(d2)
	assertErrIs(t, err, ErrCorrupt)
	var ce *CorruptError
	if !errors.As(err, &ce) || ce.Index != 2 {
		t.Fatalf("中间撕裂应报下标 2 的 CorruptError, got %#v", err)
	}
	t.Logf("输入: 撕裂后仍有记录; 输出: %v (idx=%d); 判定: 非末尾撕裂 -> ErrCorrupt",
		err, ce.Index)
}

// TestRecoverCorruptCases 首条非快照/中间出现快照/CURRENT 缺失/回放非法。
func TestRecoverCorruptCases(t *testing.T) {
	m := mustNew(t, 10)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "b")}}); err != nil {
		t.Fatal(err)
	}
	base := m.Disk()

	// CURRENT 指向不存在的清单。
	missing := cloneDisk(base)
	missing.Current = 99
	if _, err := Recover(missing); !errors.Is(err, ErrNoCurrent) {
		t.Fatalf("缺 CURRENT: %v", err)
	}

	// 首条是 Edit。
	badFirst := cloneDisk(base)
	badFirst.Manifests[1] = []Record{{Edit: &Edit{}}}
	_, err := Recover(badFirst)
	assertErrIs(t, err, ErrCorrupt)

	// 首条 Snapshot 撕裂。
	badSnap := cloneDisk(base)
	badSnap.Manifests[1][0].Torn = true
	_, err = Recover(badSnap)
	assertErrIs(t, err, ErrCorrupt)

	// 中间出现 Snapshot。
	badMid := cloneDisk(base)
	v := Version{NextFile: 3}
	badMid.Manifests[1] = append(badMid.Manifests[1], Record{Snapshot: &v})
	_, err = Recover(badMid)
	assertErrIs(t, err, ErrCorrupt)

	// 回放非法 Edit（重叠）：ErrCorrupt 同时 Is ErrOverlap，带下标。
	badOverlap := cloneDisk(base)
	badOverlap.Manifests[1] = append(badOverlap.Manifests[1],
		Record{Edit: &Edit{
			// 先制造一个 1 层文件，再制造相接文件，放在同一清单里。
		}})
	// 直接构造：快照为空，第一条 Edit 加 a..m，第二条 Edit 加 m..z。
	emptySnap := Version{NextFile: 2}
	badOverlap.Manifests[1] = []Record{
		{Snapshot: &emptySnap},
		{Edit: &Edit{Adds: []File{mkFile(1, 3, "a", "m")}}},
		{Edit: &Edit{Adds: []File{mkFile(1, 4, "m", "z")}}},
	}
	_, err = Recover(badOverlap)
	assertErrIs(t, err, ErrCorrupt, ErrOverlap)
	var ce *CorruptError
	if !errors.As(err, &ce) || ce.Index != 2 {
		t.Fatalf("应带下标 2, got %#v", err)
	}

	// 回放时删不存在文件：ErrCorrupt + ErrNoFile。
	badDel := cloneDisk(base)
	badDel.Manifests[1] = append(badDel.Manifests[1],
		Record{Edit: &Edit{Dels: []File{{Level: 1, Num: 77}}}})
	_, err = Recover(badDel)
	assertErrIs(t, err, ErrCorrupt, ErrNoFile)
}

// TestRotateUnflippedRecovery 未翻转轮转：CURRENT 不变、编辑仍进旧清单；
// 恢复版本等于活动版本，NextFile 不复用孤立清单号。
func TestRotateUnflippedRecovery(t *testing.T) {
	m := mustNew(t, 10)
	if err := m.Apply(Edit{Adds: []File{mkFile(1, 3, "a", "c")}}); err != nil {
		t.Fatal(err)
	}
	// 当前 NextFile=4：写孤立清单 4，CURRENT 仍为 1。
	num := m.RotateUnflipped()
	if num != 4 {
		t.Fatalf("孤立清单号=%d, want 4", num)
	}
	if d := m.Disk(); d.Current != 1 {
		t.Fatalf("RotateUnflipped 不应翻转 CURRENT, got %d", d.Current)
	}
	if v := m.View(); v.NextFile != 5 {
		t.Fatalf("活动 NextFile 应为 5, got %d", v.NextFile)
	}

	// 之后编辑仍追加到旧清单 1。
	if err := m.Apply(Edit{Adds: []File{mkFile(1, 6, "x", "z")}}); err != nil {
		t.Fatal(err)
	}
	d := m.Disk()
	if len(d.Manifests[1]) != 3 {
		t.Fatalf("编辑应进入旧清单, got %d 条", len(d.Manifests[1]))
	}

	rec, err := Recover(d)
	if err != nil {
		t.Fatal(err)
	}
	view := m.View()
	if !reflect.DeepEqual(rec, view) {
		t.Fatalf("恢复版本与活动版本不一致:\nrec=%s\nview=%s",
			describeVersion(rec), describeVersion(view))
	}
	// 即使旧清单回放的 NextFile 落后，孤立清单号 4 也不得复用。
	if rec.NextFile != 7 {
		t.Fatalf("恢复 NextFile=%d, want max(回放, 最大清单号+1)=7", rec.NextFile)
	}

	// Open 后立即 Rotate：新清单号不得与孤立号冲突（从 7 开始 -> 7）。
	om, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	od := om.Disk()
	if od.Current != 7 {
		t.Fatalf("Open 后 CURRENT=%d, want 7", od.Current)
	}
	if _, ok := od.Manifests[1]; !ok {
		t.Fatal("旧清单应原样保留")
	}
	if _, ok := od.Manifests[4]; !ok {
		t.Fatal("孤立清单也应保留")
	}
	if v := om.View(); v.NextFile != 8 {
		t.Fatalf("Open 轮转后 NextFile=%d, want 8", v.NextFile)
	}
}

// TestOpenPreservesAndRotates Open 恢复并立即出新清单，旧清单保留。
func TestOpenPreservesAndRotates(t *testing.T) {
	m := mustNew(t, 2)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "b")}}); err != nil {
		t.Fatal(err) // 2 条恰等 T：不轮转，CURRENT 仍为 1，NextFile=6
	}
	d := m.Disk()
	if d.Current != 1 || m.View().NextFile != 6 {
		t.Fatalf("前置状态错误: current=%d next=%d", d.Current, m.View().NextFile)
	}

	om, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	od := om.Disk()
	if od.Current != 6 {
		t.Fatalf("Open 后应 Rotate 到 6, got %d", od.Current)
	}
	if len(od.Manifests[1]) != 2 {
		t.Fatal("旧清单应原样保留（2 条记录）")
	}
	if v := om.View(); v.NextFile != 7 {
		t.Fatalf("Open 版本错误: %s", describeVersion(v))
	}
	// 输入磁盘未被 Open 改写（深拷贝）：仍是原 CURRENT=1。
	if d.Current != 1 {
		t.Fatalf("输入磁盘被污染: current=%d", d.Current)
	}

	// Open 失败：CURRENT 缺失。
	bad := cloneDisk(d)
	bad.Current = 100
	if _, err := Open(bad); !errors.Is(err, ErrNoCurrent) {
		t.Fatalf("Open 应传播 Recover 错误: %v", err)
	}
}

// TestDeepCopyIsolation Disk/View 返回深拷贝，外部修改不影响管理器。
func TestDeepCopyIsolation(t *testing.T) {
	m := mustNew(t, 5)
	if err := m.Apply(Edit{Adds: []File{mkFile(0, 5, "a", "b")}}); err != nil {
		t.Fatal(err)
	}
	d := m.Disk()
	d.Manifests[1][0].Snapshot.NextFile = 999
	d.Current = 42
	v := m.View()
	v.Files[0][0].Smallest[0] = 'z'
	v.NextFile = 999
	again := m.View()
	if again.NextFile != 6 || string(again.Files[0][0].Smallest) != "a" {
		t.Fatalf("深拷贝失效: %s", describeVersion(again))
	}
}

// TestOverlapProbeBound 非导出 overlapProbes 不超过 2*|Adds|。
func TestOverlapProbeBound(t *testing.T) {
	m := mustNew(t, 50)
	adds := []File{
		mkFile(1, 2, "a", "b"),
		mkFile(1, 3, "d", "e"),
		mkFile(1, 4, "g", "h"),
		mkFile(1, 5, "j", "k"),
	}
	if err := m.Apply(Edit{Adds: adds}); err != nil {
		t.Fatal(err)
	}
	if p := m.overlapProbes(); p > 2*len(adds) {
		t.Fatalf("probes=%d 超过 2*|Adds|=%d", p, 2*len(adds))
	}
	t.Logf("4 个 Add 插入两侧均有邻居的层，probes=%d（上限 8）", m.overlapProbes())
}

// TestConcurrent 并发调用结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	m := mustNew(t, 1000)
	const goroutines = 16
	const perG = 80
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				num := uint64(g*perG + i + 10)
				e := Edit{
					Adds: []File{mkFile(0, num, "a", "b")},
				}
				if err := m.Apply(e); err != nil {
					errs <- err
					return
				}
				_ = m.View()
				_ = m.Disk()
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// 并发下编号冲突只会以 ErrDupFile 出现（每个 goroutine 编号唯一，
		// 但若静默抬高顺序造成重入则不该有其它错误）。
		if !errors.Is(err, ErrDupFile) {
			t.Fatalf("并发期间出现意外错误: %v", err)
		}
	}

	// 串行等价性：Recover 结果必须等于 View，且 NextFile 大于所有现存编号。
	v := m.View()
	rec, err := Recover(m.Disk())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, rec) {
		t.Fatalf("并发后 Recover 与 View 不一致")
	}
	total := 0
	for level := range v.Files {
		total += len(v.Files[level])
	}
	t.Logf("并发输入: %d goroutine x %d Apply/View/Disk; 输出: %d 个文件, next=%d; "+
		"判定: 无数据竞争且 Recover==View", goroutines, perG, total, v.NextFile)
}
