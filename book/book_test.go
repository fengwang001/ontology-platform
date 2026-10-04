package book_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/book"
)

func mustNew(t *testing.T, ref, tc, tu, lo, hi, m int64) *book.Book {
	t.Helper()
	b, err := book.New(ref, tc, tu, lo, hi, m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: err=%v, want errors.Is %v", what, got, want)
	}
}

func TestNewValidation(t *testing.T) {
	bad := [][6]int64{
		{0, 1, 2, 1, 10, 1},            // ref<1
		{1e9 + 1, 1, 2, 1, 1e9 + 1, 1}, // ref>1e9
		{5, 1, 2, 0, 10, 1},            // lo<1
		{5, 1, 2, 1, 1e9 + 1, 1},       // hi>1e9
		{5, 1, 2, 6, 10, 1},            // lo>ref
		{5, 1, 2, 1, 4, 1},             // ref>hi
		{5, -1, 2, 1, 10, 1},           // tc<0
		{5, 1, 1e12 + 1, 1, 10, 1},     // tu>1e12
		{5, 3, 2, 1, 10, 1},            // tc>tu
		{5, 1, 2, 1, 10, 0},            // m<1
		{5, 1, 2, 1, 10, 1e5 + 1},      // m>1e5
	}
	for i, a := range bad {
		if _, err := book.New(a[0], a[1], a[2], a[3], a[4], a[5]); !errors.Is(err, book.ErrParam) {
			t.Fatalf("case %d: err=%v, want ErrParam", i, err)
		}
	}
	if _, err := book.New(5, 0, 0, 5, 5, 1); err != nil {
		t.Fatalf("边界值应合法: %v", err)
	}
}

func TestPhaseBoundaries(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 10)
	if err := b.Submit(0, "b1", "a", true, 100, 10); err != nil {
		t.Fatal(err)
	}
	if err := b.Cancel(9, "b1"); err != nil {
		t.Fatalf("可撤期最后一刻应可撤: %v", err)
	}
	if err := b.Submit(9, "b2", "a", true, 100, 10); err != nil {
		t.Fatal(err)
	}
	wantErr(t, b.Cancel(10, "b2"), book.ErrPhase, "Tc 起不可撤")
	if err := b.Submit(10, "s1", "a", false, 100, 5); err != nil {
		t.Fatalf("不可撤期仍可报单: %v", err)
	}
	if err := b.Submit(19, "s2", "a", false, 100, 5); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := b.Uncross(19)
	wantErr(t, err, book.ErrPhase, "now<Tu 不可开盘")
	p, trades, _, err := b.Uncross(20)
	if err != nil {
		t.Fatalf("now=Tu 应可开盘: %v", err)
	}
	if p != 100 || len(trades) != 2 {
		t.Fatalf("p=%d trades=%v", p, trades)
	}
	wantErr(t, b.Submit(20, "x", "a", true, 100, 1), book.ErrPhase, "已开盘拒绝报单")
	wantErr(t, b.Cancel(20, "b2"), book.ErrPhase, "已开盘拒绝撤单")
	_, _, _, err = b.Uncross(21)
	wantErr(t, err, book.ErrPhase, "Uncross 只能成功一次")
}

func TestPriceBandEndpoints(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 10)
	if err := b.Submit(0, "lo", "a", true, 90, 1); err != nil {
		t.Fatalf("price==lo 不越带: %v", err)
	}
	if err := b.Submit(0, "hi", "a", false, 110, 1); err != nil {
		t.Fatalf("price==hi 不越带: %v", err)
	}
	wantErr(t, b.Submit(0, "lo-", "a", true, 89, 1), book.ErrPriceBand, "price<lo")
	wantErr(t, b.Submit(0, "hi+", "a", false, 111, 1), book.ErrPriceBand, "price>hi")
}

func TestAccountLimitAndRelease(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 2)
	for _, id := range []string{"o1", "o2"} {
		if err := b.Submit(0, id, "acct", true, 100, 1); err != nil {
			t.Fatal(err)
		}
	}
	wantErr(t, b.Submit(0, "o3", "acct", true, 100, 1), book.ErrAccountLimit, "达上限")
	if err := b.Submit(0, "o3", "other", true, 100, 1); err != nil {
		t.Fatalf("其他账户不受限: %v", err)
	}
	if err := b.Cancel(1, "o1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Submit(1, "o4", "acct", true, 100, 1); err != nil {
		t.Fatalf("撤单释放名额后应可报单: %v", err)
	}
}

func TestRejectOrder(t *testing.T) {
	// 参数非法 > 时钟回退 > 阶段不符 > 编号重复/不存在 > 价格越带 > 账户超限。
	b := mustNew(t, 100, 10, 20, 90, 110, 1)
	if err := b.Submit(5, "dup", "a", true, 100, 1); err != nil {
		t.Fatal(err)
	}
	wantErr(t, b.Submit(4, "", "", true, 0, 0), book.ErrParam, "参数先于时钟")
	wantErr(t, b.Submit(4, "dup", "a", true, 100, 1), book.ErrClock, "时钟先于编号")
	wantErr(t, b.Cancel(4, "ghost"), book.ErrClock, "Cancel 时钟先于编号")
	if err := b.Cancel(5, "dup"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, b.Cancel(5, "dup"), book.ErrUnknownID, "已撤视为不存在")
	wantErr(t, b.Cancel(5, "ghost"), book.ErrUnknownID, "编号不存在")
	wantErr(t, b.Submit(5, "dup", "a", true, 100, 1), book.ErrDuplicateID, "撤过的 id 仍重复")
	if err := b.Submit(6, "inband", "a", true, 100, 1); err != nil {
		t.Fatal(err)
	}
	wantErr(t, b.Cancel(10, "ghost"), book.ErrPhase, "不可撤期阶段先于编号不存在")
	wantErr(t, b.Submit(10, "inband", "a", true, 200, 1), book.ErrDuplicateID, "编号先于价格带")
	wantErr(t, b.Submit(10, "newid", "a", true, 200, 1), book.ErrPriceBand, "价格带先于账户超限")
	wantErr(t, b.Submit(10, "newid2", "a", true, 100, 1), book.ErrAccountLimit, "账户超限")
}

func TestRejectedOpsKeepState(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 10)
	if err := b.Submit(50, "ok", "a", true, 100, 7); err != nil {
		t.Fatal(err)
	}
	// 被拒操作不推进时钟：now=99 的重复提交被拒后，now=60 的合法操作仍应接受。
	wantErr(t, b.Submit(99, "ok", "a", true, 100, 1), book.ErrDuplicateID, "重复编号")
	if err := b.Submit(60, "next", "a", false, 100, 7); err != nil {
		t.Fatalf("被拒操作不得推进时钟: %v", err)
	}
	// 被拒操作不占序号：ok=1, next=2。
	_, trades, remaining, err := b.Uncross(60)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].BuySeq != 1 || trades[0].SellSeq != 2 {
		t.Fatalf("序号应连续: trades=%+v", trades)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining=%+v", remaining)
	}
}

func TestUncrossExample(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 10)
	type sub struct {
		id    string
		buy   bool
		price int64
		qty   int64
	}
	subs := []sub{
		{"b1", true, 102, 300}, {"b2", true, 101, 200}, {"b3", true, 100, 400},
		{"s1", false, 99, 200}, {"s2", false, 100, 300}, {"s3", false, 101, 300},
		{"s4", false, 103, 100},
	}
	for i, s := range subs {
		if err := b.Submit(int64(i), s.id, "a", s.buy, s.price, s.qty); err != nil {
			t.Fatal(err)
		}
	}
	p, trades, remaining, err := b.Uncross(20)
	if err != nil {
		t.Fatal(err)
	}
	if p != 101 {
		t.Fatalf("开盘价=%d, want 101", p)
	}
	type tr struct {
		buy, sell string
		qty       int64
	}
	got := []tr{}
	for _, x := range trades {
		if x.Price != 101 {
			t.Fatalf("成交价=%d, want 101", x.Price)
		}
		got = append(got, tr{x.BuyID, x.SellID, x.Qty})
	}
	want := []tr{{"b1", "s1", 200}, {"b1", "s2", 100}, {"b2", "s2", 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trades=%v, want %v", got, want)
	}
	gotRem := []tr{}
	for _, o := range remaining {
		gotRem = append(gotRem, tr{o.ID, "", o.Qty})
	}
	wantRem := []tr{{"b3", "", 400}, {"s3", "", 300}, {"s4", "", 100}}
	if !reflect.DeepEqual(gotRem, wantRem) {
		t.Fatalf("remaining=%v, want %v", gotRem, wantRem)
	}
}

func TestUncrossNoTrade(t *testing.T) {
	b := mustNew(t, 100, 10, 20, 90, 110, 10)
	if err := b.Submit(0, "b1", "a", true, 95, 10); err != nil {
		t.Fatal(err)
	}
	if err := b.Submit(0, "s1", "a", false, 105, 10); err != nil {
		t.Fatal(err)
	}
	p, trades, remaining, err := b.Uncross(20)
	if err != nil {
		t.Fatal(err)
	}
	if p != 0 || len(trades) != 0 || len(remaining) != 2 {
		t.Fatalf("p=%d trades=%v remaining=%v", p, trades, remaining)
	}
	if remaining[0].ID != "b1" || remaining[1].ID != "s1" {
		t.Fatalf("剩余清单应按序号升序: %v", remaining)
	}
}

func TestReplayDeterministic(t *testing.T) {
	run := func() (int64, []book.Trade, []book.Order) {
		b := mustNew(t, 100, 5, 10, 90, 110, 100)
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("o%d", i)
			_ = b.Submit(int64(i%5), id, fmt.Sprintf("a%d", i%3),
				i%2 == 0, int64(95+i%10), int64(10+i))
		}
		_ = b.Cancel(4, "o3")
		p, tr, rem, err := b.Uncross(10)
		if err != nil {
			t.Fatal(err)
		}
		return p, tr, rem
	}
	p1, t1, r1 := run()
	p2, t2, r2 := run()
	if p1 != p2 || !reflect.DeepEqual(t1, t2) || !reflect.DeepEqual(r1, r2) {
		t.Fatal("相同操作序列重放结果不一致")
	}
}

func TestConcurrent(t *testing.T) {
	b := mustNew(t, 100, 50, 100, 90, 110, 100000)
	var accepted, qtySum atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				n := g*250 + i
				q := int64(1 + n%100)
				err := b.Submit(0, fmt.Sprintf("g%d-%d", g, i),
					"acct", n%2 == 0, int64(90+n%21), q)
				if err == nil {
					accepted.Add(1)
					qtySum.Add(q)
				}
			}
		}(g)
	}
	wg.Wait()
	if accepted.Load() != 2000 {
		t.Fatalf("accepted=%d, want 2000", accepted.Load())
	}
	_, trades, remaining, err := b.Uncross(100)
	if err != nil {
		t.Fatal(err)
	}
	var traded, remQty int64
	for _, tr := range trades {
		traded += tr.Qty
	}
	for _, o := range remaining {
		remQty += o.Qty
	}
	if remQty+2*traded != qtySum.Load() {
		t.Fatalf("量不守恒: 剩余 %d + 双边成交 %d != 报单总量 %d",
			remQty, 2*traded, qtySum.Load())
	}
}
