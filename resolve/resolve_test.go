package resolve

import (
	"errors"
	"fmt"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
)

func mustCreate(t *testing.T, r *indexreg.Registry, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := r.CreateIndex(n); err != nil {
			t.Fatalf("CreateIndex(%q): %v", n, err)
		}
	}
}

func mustUpdate(t *testing.T, r *indexreg.Registry, actions ...alias.Action) {
	t.Helper()
	if err := alias.Update(r, actions); err != nil {
		t.Fatalf("Update(%v): %v", actions, err)
	}
}

// TestWriteIndexDerivation 复现规格示例：推定三情形、单成员显式假、同批交换写索引。
func TestWriteIndexDerivation(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2")

	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteUnspecified, nil))
	if got, err := ResolveWrite(r, "a"); err != nil || got != "i1" {
		t.Fatalf("单成员未指定应推定为写索引: got %q, %v", got, err)
	}

	mustUpdate(t, r, alias.Add("a", "i2", indexreg.WriteUnspecified, nil))
	if _, err := ResolveWrite(r, "a"); !errors.Is(err, ErrNoWriteIndex) {
		t.Fatalf("多成员都未指定应无写索引: %v", err)
	}
	targets, err := ResolveRead(r, "a")
	if err != nil || len(targets) != 2 || targets[0].Index != "i1" || targets[1].Index != "i2" {
		t.Fatalf("ResolveRead(a) = %+v, %v", targets, err)
	}

	mustUpdate(t, r, alias.Add("a", "i2", indexreg.WriteTrue, nil))
	if got, err := ResolveWrite(r, "a"); err != nil || got != "i2" {
		t.Fatalf("恰一个真应取它: got %q, %v", got, err)
	}

	// 单独 Add(a,i1,真) 终态两个真，报多写索引且状态不变。
	err = alias.Update(r, []alias.Action{alias.Add("a", "i1", indexreg.WriteTrue, nil)})
	if !errors.Is(err, alias.ErrMultipleWriteIndices) {
		t.Fatalf("两个真应报多写索引: %v", err)
	}
	if got, _ := ResolveWrite(r, "a"); got != "i2" {
		t.Fatalf("被拒后写索引应仍为 i2: %q", got)
	}

	// 同批交换：中间态两个真不算错，终态只有 i1 为真。
	mustUpdate(t, r,
		alias.Add("a", "i1", indexreg.WriteTrue, nil),
		alias.Add("a", "i2", indexreg.WriteFalse, nil),
	)
	if got, err := ResolveWrite(r, "a"); err != nil || got != "i1" {
		t.Fatalf("同批交换后写索引应为 i1: got %q, %v", got, err)
	}

	// 移除 i1 后只剩 i2 且显式为假：无写索引。
	mustUpdate(t, r, alias.Remove("a", "i1", true))
	if _, err := ResolveWrite(r, "a"); !errors.Is(err, ErrNoWriteIndex) {
		t.Fatalf("单成员显式假应无写索引: %v", err)
	}
}

// TestSingleMemberExplicitFalse 单成员显式为假属于无写索引，但读解析不受影响。
func TestSingleMemberExplicitFalse(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1")
	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteFalse, nil))
	if _, err := ResolveWrite(r, "a"); !errors.Is(err, ErrNoWriteIndex) {
		t.Fatalf("got %v", err)
	}
	targets, err := ResolveRead(r, "a")
	if err != nil || len(targets) != 1 || targets[0].Index != "i1" {
		t.Fatalf("读不受写推定影响: %+v, %v", targets, err)
	}
}

// TestClosedAsymmetry 复现规格示例：关闭成员在读与写上的不对称。
func TestClosedAsymmetry(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2", "i3")
	mustUpdate(t, r, alias.RemoveIndex("i1"), alias.Add("i1", "i2", indexreg.WriteUnspecified, nil))
	if got, err := ResolveWrite(r, "i1"); err != nil || got != "i2" {
		t.Fatalf("改名后写索引应为 i2: %q, %v", got, err)
	}
	// 别名 b：写索引 i2（显式真），另有未指定成员 i3。
	mustUpdate(t, r,
		alias.Add("b", "i2", indexreg.WriteTrue, nil),
		alias.Add("b", "i3", indexreg.WriteUnspecified, nil),
	)
	if err := r.CloseIndex("i2"); err != nil {
		t.Fatal(err)
	}

	// 读：关闭成员被静默跳过，全部关闭时返回空列表而不报错。
	targets, err := ResolveRead(r, "i1")
	if err != nil || len(targets) != 0 {
		t.Fatalf("全部关闭应返回空列表: %+v, %v", targets, err)
	}
	targets, err = ResolveRead(r, "b")
	if err != nil || len(targets) != 1 || targets[0].Index != "i3" {
		t.Fatalf("关闭成员应被跳过: %+v, %v", targets, err)
	}
	// 写：写索引已关闭报索引已关闭，不回落到其他成员。
	if _, err := ResolveWrite(r, "i1"); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("写索引关闭应报索引已关闭: %v", err)
	}
	if _, err := ResolveWrite(r, "b"); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("写解析不应回落到 i3: %v", err)
	}
	// 索引自身已关闭：读与写都报索引已关闭。
	if _, err := ResolveRead(r, "i2"); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("ResolveRead(关闭索引): %v", err)
	}
	if _, err := ResolveWrite(r, "i2"); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("ResolveWrite(关闭索引): %v", err)
	}
}

// TestNameNotFound 名字既非索引也非别名时报名字不存在。
func TestNameNotFound(t *testing.T) {
	r := indexreg.New()
	for _, name := range []string{"ghost", "Bad"} {
		if _, err := ResolveRead(r, name); !errors.Is(err, indexreg.ErrNameNotFound) {
			t.Errorf("ResolveRead(%q): %v", name, err)
		}
		if _, err := ResolveWrite(r, name); !errors.Is(err, indexreg.ErrNameNotFound) {
			t.Errorf("ResolveWrite(%q): %v", name, err)
		}
	}
}

// TestReadTargets 读目标按索引名字节序，各带 filter；索引自身 filter 为 nil。
func TestReadTargets(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2", "i3")
	f1, f3 := "x>1", "y<2"
	mustUpdate(t, r,
		alias.Add("a", "i3", indexreg.WriteUnspecified, &f3),
		alias.Add("a", "i1", indexreg.WriteUnspecified, &f1),
		alias.Add("a", "i2", indexreg.WriteUnspecified, nil),
	)
	targets, err := ResolveRead(r, "a")
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadTarget{
		{Index: "i1", Filter: &f1},
		{Index: "i2", Filter: nil},
		{Index: "i3", Filter: &f3},
	}
	if len(targets) != len(want) {
		t.Fatalf("got %+v", targets)
	}
	for i, w := range want {
		got := targets[i]
		if got.Index != w.Index || (got.Filter == nil) != (w.Filter == nil) ||
			(got.Filter != nil && *got.Filter != *w.Filter) {
			t.Errorf("targets[%d] = %+v, want %+v", i, got, w)
		}
	}
	// 索引解析到它自己。
	targets, err = ResolveRead(r, "i1")
	if err != nil || len(targets) != 1 || targets[0].Index != "i1" || targets[0].Filter != nil {
		t.Fatalf("索引自身: %+v, %v", targets, err)
	}
	if got, err := ResolveWrite(r, "i1"); err != nil || got != "i1" {
		t.Fatalf("索引写自身: %q, %v", got, err)
	}
}

// TestTouchedIndependentOfMemberCount 证明 ResolveWrite 触碰的成员记录数
// 不超过 1，与别名成员数无关（2 与 2000 两档对照）。
func TestTouchedIndependentOfMemberCount(t *testing.T) {
	for _, n := range []int{2, 2000} {
		r := indexreg.New()
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("i%04d", i)
		}
		mustCreate(t, r, names...)
		// 每批至多 100 个动作，全部以未指定加入。
		for start := 0; start < n; start += 100 {
			end := start + 100
			if end > n {
				end = n
			}
			acts := make([]alias.Action, 0, end-start)
			for _, name := range names[start:end] {
				acts = append(acts, alias.Add("a", name, indexreg.WriteUnspecified, nil))
			}
			mustUpdate(t, r, acts...)
		}
		mustUpdate(t, r, alias.Add("a", names[1], indexreg.WriteTrue, nil))

		touched.Store(0)
		got, err := ResolveWrite(r, "a")
		if err != nil || got != names[1] {
			t.Fatalf("n=%d: got %q, %v", n, got, err)
		}
		if c := touched.Load(); c > 1 {
			t.Fatalf("n=%d: touched = %d, 应不超过 1", n, c)
		}
		t.Logf("成员数=%d ResolveWrite 触碰成员记录数=%d（判定依据：写索引在提交时推定并缓存，解析只读缓存槽位）", n, touched.Load())
	}
}
