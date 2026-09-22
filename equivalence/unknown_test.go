package equivalence_test

import (
	"errors"
	"testing"

	"ontology/equivalence"
)

func TestFindUnknownDoesNotCreate(t *testing.T) {
	uf := equivalence.New()
	uf.Add("a")

	rep, err := uf.Find("ghost")
	if !errors.Is(err, equivalence.ErrUnknownElement) || !equivalence.IsUnknown(err) {
		t.Fatalf("Find 未知元素 err = %v，应包装 ErrUnknownElement", err)
	}
	if rep != "" {
		t.Fatalf("未知元素代表元应为空串，得到 %q", rep)
	}
	if uf.ClassCount() != 1 {
		t.Fatalf("失败的 Find 不得隐式创建，类数 = %d", uf.ClassCount())
	}
	if _, err := uf.Find("ghost"); !errors.Is(err, equivalence.ErrUnknownElement) {
		t.Fatalf("第二次 Find 仍应报未知，得到 %v", err)
	}
}

func TestUnionImplicitlyCreates(t *testing.T) {
	uf := equivalence.New()

	rep, merged, err := uf.Union("x", "y")
	if err != nil || !merged || rep != "x" {
		t.Fatalf("Union(x,y) = (%q,%v,%v)", rep, merged, err)
	}
	if uf.ClassCount() != 1 {
		t.Fatalf("隐式创建后应恰有 1 类，得到 %d", uf.ClassCount())
	}
	if rep, err := uf.Find("y"); err != nil || rep != "x" {
		t.Fatalf("Find(y) = (%q,%v)", rep, err)
	}

	// 未知的更小 ID 隐式创建并成为代表元。
	rep, merged, err = uf.Union("y", "w")
	if err != nil || !merged || rep != "w" {
		t.Fatalf("Union(y,w) = (%q,%v,%v)", rep, merged, err)
	}
}

func TestAddIdempotentAndEmptyString(t *testing.T) {
	uf := equivalence.New()
	uf.Add("")
	uf.Add("")
	uf.Add("k")
	uf.Add("k")
	if count := uf.ClassCount(); count != 2 {
		t.Fatalf("幂等 Add 后类数 = %d，期望 2", count)
	}
	if rep, err := uf.Find(""); err != nil || rep != "" {
		t.Fatalf("空串应是合法 ID，Find = (%q,%v)", rep, err)
	}

	// 空串并入其他类后，因字典序最小而成为代表元。
	if rep, _, err := uf.Union("", "k"); err != nil || rep != "" {
		t.Fatalf("Union(\"\",k) 代表元 = %q, err = %v", rep, err)
	}
}

func TestEquivalentErrorVsFalse(t *testing.T) {
	uf := equivalence.New()
	uf.Add("a")
	uf.Add("b")

	// 已知但不连通：(false, nil)。
	connected, err := uf.Equivalent("a", "b")
	if err != nil || connected {
		t.Fatalf("已知不连通应为 (false,nil)，得到 (%v,%v)", connected, err)
	}

	// 任一未知：错误，绝不能用 false 混淆。
	for _, pair := range [][2]string{{"a", "nope"}, {"nope", "a"}, {"x", "y"}} {
		connected, err := uf.Equivalent(pair[0], pair[1])
		if !errors.Is(err, equivalence.ErrUnknownElement) {
			t.Fatalf("Equivalent%v err = %v，应为未知错误", pair, err)
		}
		if connected {
			t.Fatalf("Equivalent%v 未知时必须返回 false", pair)
		}
	}

	if _, _, err := uf.Union("a", "b"); err != nil {
		t.Fatal(err)
	}
	connected, err = uf.Equivalent("a", "b")
	if err != nil || !connected {
		t.Fatalf("合并后应为 (true,nil)，得到 (%v,%v)", connected, err)
	}
}
