package consignment

import (
	"errors"
	"testing"
)

// mustArrive 登记到货并在失败时终止测试。
func mustArrive(t *testing.T, s *System, supplier, item, qty uint64, period, at int64) uint64 {
	t.Helper()
	id, err := s.Arrive(supplier, item, qty, period, at)
	if err != nil {
		t.Fatalf("Arrive(sup=%d,item=%d,qty=%d,period=%d,t=%d) 失败: %v",
			supplier, item, qty, period, at, err)
	}
	return id
}

// mustDraw 领用并在失败时终止测试。
func mustDraw(t *testing.T, s *System, item, qty uint64, at int64) []Line {
	t.Helper()
	lines, err := s.Draw(item, qty, at)
	if err != nil {
		t.Fatalf("Draw(item=%d,qty=%d,t=%d) 失败: %v", item, qty, at, err)
	}
	return lines
}

func mustPrice(t *testing.T, s *System, supplier, item uint64, from, to, price, at int64) {
	t.Helper()
	if err := s.AddPrice(supplier, item, from, to, price, at); err != nil {
		t.Fatalf("AddPrice(sup=%d,item=%d,[%d,%d),price=%d,t=%d) 失败: %v",
			supplier, item, from, to, price, at, err)
	}
}

// TestExpiryExactBoundary 验证期限恰到与差一秒：
// 批次在 arrival+period 时刻恰好到期，差一秒时仍可领用。
func TestExpiryExactBoundary(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 1000, 5, 0)
	b := mustArrive(t, s, 1, 10, 8, 100, 50) // 到货 t=50，期限 100，到期时刻 150

	// 差一秒（t=149）：未到期，可领用 3。
	lines := mustDraw(t, s, 10, 3, 149)
	if len(lines) != 1 || lines[0].BatchID != b || lines[0].Qty != 3 {
		t.Fatalf("t=149 应从未到期批次领用 3，得到 %+v", lines)
	}

	// 恰好到期（t=150）：剩余 5 不可领用。
	if _, err := s.Draw(10, 1, 150); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("t=150 批次恰到期，应报库存不足，得到 %v", err)
	}
	// 被拒绝的领用不推进时钟，用一次被接受的空操作把时钟推到 150。
	if err := s.SetCap(1, 10, 1000, 150); err != nil {
		t.Fatal(err)
	}
	total, expired := s.OnHand(1, 10)
	if total != 5 || expired != 5 {
		t.Fatalf("t=150 在库 5 应全部到期，得到 total=%d expired=%d", total, expired)
	}

	// 到期批次须由退回移出。
	if err := s.Return(b, 5, 151); err != nil {
		t.Fatalf("退回到期批次失败: %v", err)
	}
	total, expired = s.OnHand(1, 10)
	if total != 0 || expired != 0 {
		t.Fatalf("退回后在库应为 0，得到 total=%d expired=%d", total, expired)
	}
}

// TestCapExactAndOver 验证在库上限恰等于允许、超一则整批拒绝。
func TestCapExactAndOver(t *testing.T) {
	s := New()
	if err := s.SetCap(1, 10, 100, 0); err != nil {
		t.Fatalf("SetCap 失败: %v", err)
	}
	// 到货 60 + 40 = 100，恰等于上限，允许。
	mustArrive(t, s, 1, 10, 60, 1000, 1)
	mustArrive(t, s, 1, 10, 40, 1000, 2)
	// 再到货 1，101 > 100，整批拒绝。
	if _, err := s.Arrive(1, 10, 1, 1000, 3); !errors.Is(err, ErrOverCap) {
		t.Fatalf("超出上限 1 应报超上限，得到 %v", err)
	}
	// 被拒绝的到货不改变在库量。
	if total, _ := s.OnHand(1, 10); total != 100 {
		t.Fatalf("被拒绝的到货不得改变在库量，得到 %d", total)
	}
	// 已到期未退回部分仍占上限额度。
	s2 := New()
	if err := s2.SetCap(1, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	mustArrive(t, s2, 1, 10, 10, 5, 0) // t=5 起即到期，但未退回
	if _, err := s2.Arrive(1, 10, 1, 5, 6); !errors.Is(err, ErrOverCap) {
		t.Fatalf("已到期未退回部分仍占额度，应报超上限，得到 %v", err)
	}
}

// TestFIFOTieBreak 验证跨供应商统一排序：
// 到货时刻早者先；并列取供应商编号小者；再并列取批次号小者。
func TestFIFOTieBreak(t *testing.T) {
	s := New()
	for _, sup := range []uint64{1, 2, 3} {
		mustPrice(t, s, sup, 10, 0, 1000, int64(sup), 0)
	}
	// 同一到货时刻 t=10，供应商 3 先到货（批次号小），供应商 1 后到货。
	bS3 := mustArrive(t, s, 3, 10, 5, 1000, 10)   // t=10 供应商 3
	bS1 := mustArrive(t, s, 1, 10, 5, 1000, 10)   // t=10 供应商 1
	bS2a := mustArrive(t, s, 2, 10, 5, 1000, 10)  // t=10 供应商 2 批次 a
	bS2b := mustArrive(t, s, 2, 10, 5, 1000, 10)  // t=10 供应商 2 批次 b
	bLate := mustArrive(t, s, 2, 10, 5, 1000, 20) // 时刻最晚，最后分配

	// 领用 12：顺序应为 bS1(5) -> bS2a(5) -> bS2b(2)。
	lines := mustDraw(t, s, 10, 12, 30)
	want := []struct {
		batch uint64
		qty   uint64
		price int64
	}{
		{bS1, 5, 1}, {bS2a, 5, 2}, {bS2b, 2, 2},
	}
	if len(lines) != len(want) {
		t.Fatalf("结算行数应为 %d，得到 %+v", len(want), lines)
	}
	for i, w := range want {
		if lines[i].BatchID != w.batch || lines[i].Qty != w.qty || lines[i].UnitPrice != w.price {
			t.Fatalf("第 %d 行应为 batch=%d qty=%d price=%d，得到 %+v",
				i, w.batch, w.qty, w.price, lines[i])
		}
	}
	// 再领用 8：bS2b 余 3 -> bS3(5)；bLate 时刻最晚不动。
	lines = mustDraw(t, s, 10, 8, 31)
	if len(lines) != 2 || lines[0].BatchID != bS2b || lines[0].Qty != 3 ||
		lines[1].BatchID != bS3 || lines[1].Qty != 5 {
		t.Fatalf("第二笔领用分配次序错误: %+v", lines)
	}
	// 只剩 bLate 的 5。
	lines = mustDraw(t, s, 10, 5, 32)
	if len(lines) != 1 || lines[0].BatchID != bLate {
		t.Fatalf("最后应只剩最晚批次，得到 %+v", lines)
	}
	if _, err := s.Draw(10, 1, 33); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("库存耗尽应报库存不足，得到 %v", err)
	}
}
