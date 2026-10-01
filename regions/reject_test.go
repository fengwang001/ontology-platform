package regions

import "testing"

func TestRejectionReasons(t *testing.T) {
	s := NewSet()

	// 坐标越界。
	err := s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {1, 0}, {maxCoord + 1, 1}}})
	expectErr(t, err, ErrCoordOutOfRange, "Put 外环坐标越界")

	// 洞坐标越界，即使外环合法也先报越界。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Hole: Ring{{1, 1}, {2, 1}, {-maxCoord - 1, 2}}})
	expectErr(t, err, ErrCoordOutOfRange, "Put 洞坐标越界")

	// 顶点数不足（先于凸性检查）。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {1, 0}}})
	expectErr(t, err, ErrTooFewVertices, "Put 外环顶点数<3")

	// 洞顶点数不足（外环合法时才轮到）。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Hole: Ring{{1, 1}, {2, 2}}})
	expectErr(t, err, ErrTooFewVertices, "Put 洞顶点数<3")

	// 外环非严格凸（共线）。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {4, 0}, {8, 0}, {8, 8}, {0, 8}}})
	expectErr(t, err, ErrOuterNotConvex, "Put 外环共线非严格凸")

	// 五角星：每个顶点转向同号、整圈绕两周的自交序列，必须拒绝。
	star := Ring{{3, 0}, {0, 4}, {5, 1}, {1, 1}, {6, 4}}
	t.Logf("输入: 五角星顶点序列 %v", []Point(star))
	// 先证明它在“相邻三顶点转向”这一层全为同号非零，
	// 即必须由“整圈只绕一周 / 全局半平面”检查才能拒绝。
	s0 := crossAt(star, 0).Sign()
	for i := range star {
		if crossAt(star, i).Sign() != s0 {
			t.Fatalf("test premise broken: star turn signs differ at %d", i)
		}
	}
	if strictlyConvex(star) {
		t.Fatal("pentagram double-winding sequence must not be strictly convex")
	}
	err = s.Put(Region{ID: "bad", Outer: star})
	expectErr(t, err, ErrOuterNotConvex, "Put 整圈绕两周的五角星顶点序列")

	// 洞非严格凸（共线）。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Hole: Ring{{2, 2}, {4, 2}, {6, 2}, {6, 6}, {2, 6}}})
	expectErr(t, err, ErrHoleNotConvex, "Put 洞非严格凸")

	// 洞顶点落在外环边上。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Hole: Ring{{0, 4}, {2, 2}, {2, 6}}})
	expectErr(t, err, ErrHoleNotInside, "Put 洞顶点落在外环边上")

	// 洞顶点在外环外部。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Hole: Ring{{7, 7}, {9, 7}, {7, 9}}})
	expectErr(t, err, ErrHoleNotInside, "Put 洞顶点在外环外部")

	// 检查顺序：外环不凸时，即便洞也有问题，仍报外环。
	err = s.Put(Region{ID: "bad", Outer: Ring{{0, 0}, {1, 0}}, Hole: Ring{{0, 0}, {1, 0}}})
	expectErr(t, err, ErrTooFewVertices, "Put 外环顶点数优先于洞检查")

	// Locate 查询坐标越界，同一原因。
	_, err = s.Locate(maxCoord+1, 0)
	expectErr(t, err, ErrCoordOutOfRange, "Locate 查询坐标越界")
	_, err = s.Locate(0, -maxCoord-1)
	expectErr(t, err, ErrCoordOutOfRange, "Locate 查询坐标越界(负)")

	// Replace/Remove 不存在的 id。
	err = s.Replace(Region{ID: "ghost", Outer: Ring{{0, 0}, {1, 0}, {0, 1}}})
	expectErr(t, err, ErrIDNotFound, "Replace id 不存在")
	err = s.Remove("ghost")
	expectErr(t, err, ErrIDNotFound, "Remove id 不存在")
}

func TestPutExistsAndReplaceFailureAtomic(t *testing.T) {
	s := NewSet()
	old := Region{ID: "r", Priority: 1, Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}}}
	mustPut(t, s, old)

	err := s.Put(old)
	expectErr(t, err, ErrIDExists, "Put 已存在 id")

	// Replace 给出非法几何 -> 几何错误先于 id 检查且原区域保留。
	bad := Region{ID: "r", Priority: 9, Outer: Ring{{0, 0}, {1, 0}}}
	err = s.Replace(bad)
	expectErr(t, err, ErrTooFewVertices, "Replace 非法几何")

	// Replace 合法但 id 不存在。
	err = s.Replace(Region{ID: "other", Priority: 9, Outer: Ring{{0, 0}, {1, 0}, {0, 1}}})
	expectErr(t, err, ErrIDNotFound, "Replace 不存在 id")

	// 原区域仍可定位，优先级与形状不变。
	best, ok, err := s.Best(4, 4)
	if err != nil || !ok || best.ID != "r" || best.Priority != 1 {
		t.Fatalf("after failed Replace, original missing: %+v ok=%v err=%v", best, ok, err)
	}
	t.Logf("输入: Replace 失败后 Best(4,4) -> 输出: %s prio=%d；判定依据: 失败操作不改变集合",
		best.ID, best.Priority)

	// 成功的 Replace 原子生效。
	nr := Region{ID: "r", Priority: 5, Outer: Ring{{10, 0}, {18, 0}, {18, 8}, {10, 8}}}
	if err := s.Replace(nr); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := s.Locate(4, 4); len(got) != 0 {
		t.Fatalf("old region still locatable after Replace: %v", ids(got))
	}
	best, ok, _ = s.Best(14, 4)
	if !ok || best.Priority != 5 {
		t.Fatal("new region not locatable after Replace")
	}
	t.Logf("输入: Replace 成功后 Best(14,4) -> 输出: %s prio=%d", best.ID, best.Priority)

	// Remove 后不可定位。
	if err := s.Remove("r"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got, _ := s.Locate(14, 4); len(got) != 0 {
		t.Fatalf("region still locatable after Remove: %v", ids(got))
	}
	expectErr(t, s.Remove("r"), ErrIDNotFound, "Remove 已删除 id")
}
