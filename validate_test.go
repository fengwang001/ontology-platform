package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestOrderErrorCarriesWhichAndPosition(t *testing.T) {
	good := Vector{{1, 1}, {2, 2}}

	// 第一个向量下标相等。
	badA := Vector{{5, 1}, {5, 2}}
	_, _, err := Dot(badA, good)
	var oe *OrderError
	if !errors.As(err, &oe) {
		t.Fatalf("期望 *OrderError，得到 %v", err)
	}
	if oe.Which != 1 || oe.Position != 1 || oe.Prev != 5 || oe.Cur != 5 {
		t.Fatalf("OrderError 定位错误: %+v", oe)
	}

	// 第二个向量下标递减，错误位置在第 2 个元素。
	badB := Vector{{1, 1}, {9, 9}, {3, 3}}
	_, _, err = Dot(good, badB)
	if !errors.As(err, &oe) {
		t.Fatalf("期望 *OrderError，得到 %v", err)
	}
	if oe.Which != 2 || oe.Position != 2 || oe.Prev != 9 || oe.Cur != 3 {
		t.Fatalf("OrderError 定位错误: %+v", oe)
	}

	// 余弦同样带定位。
	_, _, err = Cosine(good, badB)
	if !errors.As(err, &oe) || oe.Which != 2 || oe.Position != 2 {
		t.Fatalf("Cosine 的 OrderError 定位错误: %v", err)
	}
}

func TestNaNErrorCarriesWhichAndPosition(t *testing.T) {
	good := Vector{{1, 1}}

	_, _, err := Dot(Vector{{0, math.NaN()}}, good)
	var ne *NaNError
	if !errors.As(err, &ne) {
		t.Fatalf("期望 *NaNError，得到 %v", err)
	}
	if ne.Which != 1 || ne.Position != 0 || ne.Index != 0 {
		t.Fatalf("NaNError 定位错误: %+v", ne)
	}

	_, _, err = Dot(good, Vector{{0, 1}, {4, math.NaN()}})
	if !errors.As(err, &ne) {
		t.Fatalf("期望 *NaNError，得到 %v", err)
	}
	if ne.Which != 2 || ne.Position != 1 || ne.Index != 4 {
		t.Fatalf("NaNError 定位错误: %+v", ne)
	}
}

func TestInputsAreNotModified(t *testing.T) {
	a := Vector{{0, 1.5}, {3, -2}, {9, 0}, {100, 4}}
	b := Vector{{3, 7}, {9, 1}, {100, -1}}
	aCopy := append(Vector(nil), a...)
	bCopy := append(Vector(nil), b...)

	if _, _, err := Dot(a, b); err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	assertVectorEqual(t, "a", a, aCopy)
	assertVectorEqual(t, "b", b, bCopy)

	// 未排序的输入报错后也必须保持原样（不得就地排序）。
	unsorted := Vector{{9, 1}, {3, 2}, {3, 3}}
	uCopy := append(Vector(nil), unsorted...)
	if _, _, err := Dot(unsorted, b); err == nil {
		t.Fatal("未排序输入应报错")
	}
	assertVectorEqual(t, "unsorted", unsorted, uCopy)
}

func assertVectorEqual(t *testing.T, name string, got, want Vector) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 长度被修改: %d -> %d", name, len(want), len(got))
	}
	for i := range got {
		if got[i].Index != want[i].Index ||
			math.Float64bits(got[i].Value) != math.Float64bits(want[i].Value) {
			t.Fatalf("%s 第 %d 个元素被修改: %+v -> %+v",
				name, i, want[i], got[i])
		}
	}
}

func TestDotRepeatDeterministic(t *testing.T) {
	a := Vector{{0, 1e16}, {2, 1}, {5, -1e16}, {8, 3.5}}
	b := Vector{{0, 1}, {2, 1}, {5, 1}, {8, -2}}
	first, stFirst, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	for i := 0; i < 1000; i++ {
		got, st, err := Dot(a, b)
		if err != nil {
			t.Fatalf("第 %d 次 Dot 出错: %v", i, err)
		}
		if math.Float64bits(got) != math.Float64bits(first) || st != stFirst {
			t.Fatalf("第 %d 次结果不一致: got %v/%+v, want %v/%+v",
				i, got, st, first, stFirst)
		}
	}
}
