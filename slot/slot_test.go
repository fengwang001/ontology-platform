package slot

import "testing"

func TestStoreIncrementalQuantities(t *testing.T) {
	s := NewStore()
	sl, err := s.AddSlot("L", "S", 1, 10, 20, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.AddReserve("S", 10)
	if s.Avail("S") != 10 {
		t.Fatalf("avail 初始 10, got %d", s.Avail("S"))
	}
	s.ReserveOpen(sl, 6) // 两条箱
	if sl.InTransit != 6 || s.SKUUsed("S") != 6 || s.Avail("S") != 4 {
		t.Fatalf("占用后 inTransit=%d used=%d avail=%d", sl.InTransit, s.SKUUsed("S"), s.Avail("S"))
	}
	if err := s.ApplyPick(sl, 3); err != ErrShortPick {
		t.Fatalf("onHand=2 取 3 应库存不足, got %v", err)
	}
	if err := s.ApplyPick(sl, 2); err != nil {
		t.Fatalf("取 2: %v", err)
	}
	if sl.OnHand != 0 {
		t.Fatalf("onHand 应为 0, got %d", sl.OnHand)
	}
	// Confirm 短补：onHand+4，储备按任务量 6 全额扣，在途释放 6。
	s.ApplyConfirm(sl, "S", 6, 4)
	if sl.OnHand != 4 || sl.InTransit != 0 || s.ReserveOf("S") != 4 || s.Avail("S") != 4 {
		t.Fatalf("confirm 后 oh=%d it=%d reserve=%d avail=%d",
			sl.OnHand, sl.InTransit, s.ReserveOf("S"), s.Avail("S"))
	}
}

func TestStoreTouchedDedup(t *testing.T) {
	s := NewStore()
	if _, err := s.AddSlot("L", "S", 1, 10, 20, 3, 2); err != nil {
		t.Fatal(err)
	}
	s.ResetTouched()
	_ = s.Get("L")
	_ = s.Get("L") // 重复触碰去重
	s.TouchTask(7)
	s.TouchTask(7)
	if got := s.Touched(); got != 2 {
		t.Fatalf("去重后应为 2 条, got %d", got)
	}
	s.ResetTouched()
	if got := s.Touched(); got != 0 {
		t.Fatalf("reset 后应为 0, got %d", got)
	}
}

func TestStoreErrors(t *testing.T) {
	s := NewStore()
	if _, err := s.AddSlot("L", "S", 1, 10, 20, 3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSlot("L", "S", 1, 10, 20, 3, 2); err != ErrConflict {
		t.Fatalf("重复库位应冲突, got %v", err)
	}
	if s.GetSKUSlot("ZZ") != nil {
		t.Fatalf("未知 SKU 应返回 nil")
	}
}
