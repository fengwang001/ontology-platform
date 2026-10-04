package meter

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func b(s string) []byte { return []byte(s) }

func TestMonthAndRotation(t *testing.T) {
	mt, err := New(3, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if mt.Month(0) != 0 || mt.Month(999) != 0 || mt.Month(1000) != 1 || mt.Month(2500) != 2 {
		t.Fatal("month numbering")
	}
	mt.RedeemQuota(b("d"), b("a1"), 5)
	mt.RedeemQuota(b("d"), b("a2"), 10)
	if got := mt.Used(b("d"), 999); got != 2 {
		t.Fatalf("used=%d", got)
	}
	if got := mt.Used(b("d"), 1000); got != 0 {
		t.Fatalf("new month used=%d", got)
	}
	if _, ok := mt.Lookup(b("d"), b("a1"), 1000); ok {
		t.Fatal("entry should have rotated away")
	}
}

func TestNewRejectsBadParams(t *testing.T) {
	for _, c := range [][2]int64{{-1, 10}, {1001, 10}, {3, 0}, {3, 1_000_000_001}} {
		if _, err := New(c[0], c[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) err=%v", c, err)
		}
	}
}

func TestRedeemIdempotentAndGiftNotCounted(t *testing.T) {
	mt, _ := New(3, 1000)
	mt.RedeemQuota(b("d"), b("a1"), 5)
	mt.RedeemQuota(b("d"), b("a1"), 9) // 重复不覆盖首次时刻
	e, ok := mt.Lookup(b("d"), b("a1"), 9)
	if !ok || e.FirstAt != 5 || e.Kind != Quota {
		t.Fatalf("entry=%+v ok=%v", e, ok)
	}
	if mt.Used(b("d"), 9) != 1 {
		t.Fatal("quota counted twice")
	}
	mt.RedeemGift(b("d"), b("a2"), 6)
	if mt.Used(b("d"), 6) != 1 {
		t.Fatal("gift must not consume quota")
	}
	if e, ok := mt.Lookup(b("d"), b("a2"), 6); !ok || e.Kind != Gift {
		t.Fatal("gift entry missing")
	}
}

// TestTouchedBounded verifies that one Read-style lookup+redeem sequence never
// touches more than 2 unlock records, regardless of accumulated history or the
// number of subjects. Cases: 100 vs 10000 historical unlocks.
func TestTouchedBounded(t *testing.T) {
	for _, total := range []int{100, 10_000} {
		// meter 不强制 N 上限（由 paywall 判定），故可累计超过 N 篇。
		mt, _ := New(1000, 1_000_000_000) // 单月内不轮换
		for i := 0; i < total; i++ {
			mt.RedeemQuota(b("d"), []byte(fmt.Sprintf("art-%05d", i)), int64(i))
		}
		// 大量其他主体，证明与主体总数无关。
		for i := 0; i < 500; i++ {
			mt.RedeemQuota([]byte(fmt.Sprintf("other-%03d", i)), []byte("x"), 1)
		}

		mt.ResetTouched()
		if _, ok := mt.Lookup(b("d"), []byte(fmt.Sprintf("art-%05d", total-1)), int64(total)); !ok {
			t.Fatal("expected hit")
		}
		if got := mt.Touched(); got != 1 {
			t.Fatalf("hit lookup touched=%d", got)
		}

		mt.ResetTouched()
		if _, ok := mt.Lookup(b("d"), []byte("brand-new"), int64(total)); ok {
			t.Fatal("unexpected hit")
		}
		mt.RedeemQuota(b("d"), []byte("brand-new"), int64(total))
		if got := mt.Touched(); got > 2 {
			t.Fatalf("total=%d miss+redeem touched=%d, want <=2", total, got)
		}

		// 历史月份累计：换月后老账不可见，触碰仍为 2。
		mt2, _ := New(1000, 1000)
		month := int64(0)
		remaining := total
		for remaining > 0 {
			batch := 100
			if batch > remaining {
				batch = remaining
			}
			for j := 0; j < batch; j++ {
				idx := total - remaining + j
				mt2.RedeemQuota(b("d"), []byte(fmt.Sprintf("old-%05d", idx)), month*1000+500)
			}
			remaining -= batch
			month++
		}
		mt2.ResetTouched()
		now := month*1000 + 1
		if _, ok := mt2.Lookup(b("d"), b("fresh"), now); ok {
			t.Fatal("unexpected hit in new month")
		}
		mt2.RedeemQuota(b("d"), b("fresh"), now)
		if got := mt2.Touched(); got > 2 {
			t.Fatalf("history=%d touched=%d, want <=2", total, got)
		}
	}
}

func TestMergeInto(t *testing.T) {
	// N=3：用户账 a3@5(Quota),a4@15(Quota)；设备账 a1@10(Quota),a2@20(Quota),a9@7(Gift)。
	mt, _ := New(3, 1000)
	mt.RedeemQuota(b("u"), b("a3"), 5)
	mt.RedeemQuota(b("u"), b("a4"), 15)
	mt.RedeemQuota(b("d"), b("a1"), 10)
	mt.RedeemQuota(b("d"), b("a2"), 20)
	mt.RedeemGift(b("d"), b("a9"), 7)

	mt.MergeInto(b("u"), b("d"), 30)

	got := mt.Entries(b("u"), 30)
	want := map[string]struct {
		at   int64
		kind Kind
	}{
		"a3": {5, Quota},
		"a1": {10, Quota},
		"a4": {15, Quota},
		"a9": {7, Gift},
	}
	if len(got) != len(want) {
		t.Fatalf("merged entries=%v", got)
	}
	for _, e := range got {
		w, ok := want[string(e.Article)]
		if !ok || e.FirstAt != w.at || e.Kind != w.kind {
			t.Fatalf("unexpected entry %+v", e)
		}
	}
	if mt.Used(b("u"), 30) != 3 {
		t.Fatalf("used after merge=%d", mt.Used(b("u"), 30))
	}

	// 设备匿名账原样保留。
	dEntries := mt.Entries(b("d"), 30)
	if len(dEntries) != 3 {
		t.Fatalf("device book mutated: %v", dEntries)
	}

	// 同刻按文章字节序截断：再造一次同刻合并，保留字典序靠前的 N。
	mt2, _ := New(1, 1000)
	mt2.RedeemQuota(b("u2"), b("z"), 50)
	mt2.RedeemQuota(b("d2"), b("a"), 50)
	mt2.MergeInto(b("u2"), b("d2"), 60)
	es := mt2.Entries(b("u2"), 60)
	if len(es) != 1 || !bytes.Equal(es[0].Article, b("a")) {
		t.Fatalf("tie order entries=%v", es)
	}

	// 礼赠在两侧都存在时任一方为礼赠即按礼赠，且取较早时刻。
	mt3, _ := New(1, 1000)
	mt3.RedeemQuota(b("u3"), b("g"), 100)
	mt3.RedeemGift(b("d3"), b("g"), 50)
	mt3.MergeInto(b("u3"), b("d3"), 200)
	if e, ok := mt3.Lookup(b("u3"), b("g"), 200); !ok || e.Kind != Gift || e.FirstAt != 50 {
		t.Fatalf("gift union entry=%+v ok=%v", e, ok)
	}

	// 幂等：再次合并不变。
	mt.MergeInto(b("u"), b("d"), 31)
	if mt.Used(b("u"), 31) != 3 || len(mt.Entries(b("u"), 31)) != 4 {
		t.Fatal("second merge not idempotent")
	}
}
