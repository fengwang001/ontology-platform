package freeze_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/freeze"
)

// mustCode 断言 err 为 *freeze.Error 且 Code 等于 want。
func mustCode(t *testing.T, err error, want freeze.ErrCode) {
	t.Helper()
	var fe *freeze.Error
	if !errors.As(err, &fe) {
		t.Fatalf("期望 *freeze.Error(code=%d)，得到 %v", want, err)
	}
	if fe.Code != want {
		t.Fatalf("期望错误码 %d，得到 %d（%v）", want, fe.Code, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误 %v", err)
	}
}

// mustQuery 断言查询成功且快照与期望完全一致。
func mustQuery(t *testing.T, mg *freeze.Manager, acct string, tm int64, want freeze.AccountView) {
	t.Helper()
	got, err := mg.Query(acct, tm)
	mustOK(t, err)
	if got.B != want.B || got.V != want.V || !reflect.DeepEqual(got.Orders, want.Orders) {
		t.Fatalf("t=%d 查询不匹配:\n得到 %+v\n期望 %+v", tm, got, want)
	}
}

func ov(id string, a, e, exp int64) freeze.OrderView {
	return freeze.OrderView{ID: id, A: a, E: e, Exp: exp}
}

// TestExample1 覆盖例一的完整序列（全部 t=0、exp=0）。
func TestExample1(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 70, 0))
	mustOK(t, mg.Freeze("A", 0, "o3", 30, 0))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 100, V: 0,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0), ov("o2", 70, 40, 0), ov("o3", 30, 0, 0)},
	})

	// Seize(o1, 60)：o1 整令移出，B=40，e2=min(70,40)=40、e3=0，V=0。
	mustOK(t, mg.Seize("A", 0, "o1", 60))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 40, V: 0,
		Orders: []freeze.OrderView{ov("o2", 70, 40, 0), ov("o3", 30, 0, 0)},
	})

	// Unfreeze(o2, 70)：整令移出，e3=min(30,40)=30，V=10。
	mustOK(t, mg.Unfreeze("A", 0, "o2", 70))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 40, V: 10,
		Orders: []freeze.OrderView{ov("o3", 30, 30, 0)},
	})
}

// TestExample2 覆盖例二的完整序列（到期失效、SeizeQ、编号复用）。
func TestExample2(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 70, 10))
	mustOK(t, mg.Freeze("A", 0, "o3", 30, 0))

	// t=9：o2 未到期，e 为 60、40、0，V=0。
	mustQuery(t, mg, "A", 9, freeze.AccountView{
		B: 100, V: 0,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0), ov("o2", 70, 40, 10), ov("o3", 30, 0, 0)},
	})

	// t=10：o2 失效，e1=60、e3=min(30,100-60)=30，V=10。
	mustQuery(t, mg, "A", 10, freeze.AccountView{
		B: 100, V: 10,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0), ov("o3", 30, 30, 0)},
	})

	// t=10 SeizeQ(70)：o1 扣 60 整令移出、o3 扣 10 变为 20，B=30，e3=20，V=10。
	mustOK(t, mg.SeizeQ("A", 10, 70))
	mustQuery(t, mg, "A", 10, freeze.AccountView{
		B: 30, V: 10,
		Orders: []freeze.OrderView{ov("o3", 20, 20, 0)},
	})

	// t=10 SeizeQ(21) 超过 e 之和 20，被拒。
	mustCode(t, mg.SeizeQ("A", 10, 21), freeze.ErrSeizeQExceeds)

	// t=10 Freeze(o2, 5, 0)：旧 o2 已失效，编号可复用，排在 o3 之后。
	mustOK(t, mg.Freeze("A", 10, "o2", 5, 0))
	mustQuery(t, mg, "A", 10, freeze.AccountView{
		B: 30, V: 5,
		Orders: []freeze.OrderView{ov("o3", 20, 20, 0), ov("o2", 5, 5, 0)},
	})
}

// TestWaitingFreezeAndDeposit 名义金额大于余额的轮候令，存入后递补生效。
func TestWaitingFreezeAndDeposit(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 50))
	mustOK(t, mg.Freeze("A", 0, "big", 200, 0))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 50, V: 0,
		Orders: []freeze.OrderView{ov("big", 200, 50, 0)},
	})

	// 存入 100 后轮候部分递补：e=min(200,150)=150。
	mustOK(t, mg.Deposit("A", 1, 100))
	mustQuery(t, mg, "A", 1, freeze.AccountView{
		B: 150, V: 0,
		Orders: []freeze.OrderView{ov("big", 200, 150, 0)},
	})
}

// TestWaitingFreezeAfterFrontExpiry 前序令到期使后序令递补生效。
func TestWaitingFreezeAfterFrontExpiry(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "front", 80, 5))
	mustOK(t, mg.Freeze("A", 0, "back", 60, 0))
	mustQuery(t, mg, "A", 4, freeze.AccountView{
		B: 100, V: 0,
		Orders: []freeze.OrderView{ov("front", 80, 80, 5), ov("back", 60, 20, 0)},
	})

	// t=5：front 失效，back 递补为 e=min(60,100)=60，V=40。
	mustQuery(t, mg, "A", 5, freeze.AccountView{
		B: 100, V: 40,
		Orders: []freeze.OrderView{ov("back", 60, 60, 0)},
	})
}

// TestExpiryBoundary exp 恰等于 t 已失效，比 t 大 1 仍有效。
func TestExpiryBoundary(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 30, 10))
	mustOK(t, mg.Freeze("A", 0, "o2", 30, 11))

	// t=10：exp=10 的 o1 已失效，exp=11 的 o2 仍有效。
	mustQuery(t, mg, "A", 10, freeze.AccountView{
		B: 100, V: 70,
		Orders: []freeze.OrderView{ov("o2", 30, 30, 11)},
	})
}

// TestFreezeExpParam Freeze 的 exp 恰等于 t 被拒（参数非法），大 1 通过。
func TestFreezeExpParam(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustCode(t, mg.Freeze("A", 5, "bad", 10, 5), freeze.ErrInvalidParam)
	mustCode(t, mg.Freeze("A", 5, "bad", 10, 4), freeze.ErrInvalidParam)
	mustCode(t, mg.Freeze("A", 5, "bad", 10, freeze.MaxAmount+1), freeze.ErrInvalidParam)
	mustOK(t, mg.Freeze("A", 5, "ok", 10, 6))
	mustQuery(t, mg, "A", 5, freeze.AccountView{
		B: 100, V: 90,
		Orders: []freeze.OrderView{ov("ok", 10, 10, 6)},
	})
}

// TestDebitBoundary Debit 的 x 恰等于 V 通过，比 V 大 1 被拒。
func TestDebitBoundary(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	// V = 100-60 = 40。
	mustCode(t, mg.Debit("A", 0, 41), freeze.ErrInsufficientAvail)
	mustOK(t, mg.Debit("A", 0, 40))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 60, V: 0,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0)},
	})
}

// TestSeizeBoundary Seize 的 x 恰等于 e_i 通过，比 e_i 大 1 被拒（e_i < a_i 的情形）。
func TestSeizeBoundary(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 50))
	// a=80 > B=50，故 e=50 < a。
	mustOK(t, mg.Freeze("A", 0, "o1", 80, 0))
	mustCode(t, mg.Seize("A", 0, "o1", 51), freeze.ErrSeizeExceeds)
	mustOK(t, mg.Seize("A", 0, "o1", 50))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 0, V: 0,
		Orders: []freeze.OrderView{ov("o1", 30, 0, 0)},
	})
}

// TestSeizeQExactTotal SeizeQ 的 x 恰等于 e 之和通过，所有令的 e 都被扣尽。
func TestSeizeQExactTotal(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 70, 0))
	// e 之和 = 60+40 = 100，恰等于 B。
	mustOK(t, mg.SeizeQ("A", 0, 100))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 0, V: 0,
		Orders: []freeze.OrderView{ov("o2", 30, 0, 0)},
	})
}

// TestSeizeQExceedsNoPartial SeizeQ 比 e 之和大 1 被拒且不部分执行。
func TestSeizeQExceedsNoPartial(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 30, 0))
	// e 之和 = 90，x=91 被拒，状态不变。
	mustCode(t, mg.SeizeQ("A", 0, 91), freeze.ErrSeizeQExceeds)
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 100, V: 10,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0), ov("o2", 30, 30, 0)},
	})
}

// TestSeizeQPartialLast SeizeQ 跨多道令且末道只部分扣划。
func TestSeizeQPartialLast(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 200))
	mustOK(t, mg.Freeze("A", 0, "o1", 50, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o3", 70, 0))
	// e 为 50、60、70。扣 130：o1 扣 50 移出、o2 扣 60 移出、o3 扣 20 余 50。
	mustOK(t, mg.SeizeQ("A", 0, 130))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 70, V: 20,
		Orders: []freeze.OrderView{ov("o3", 50, 50, 0)},
	})
}

// TestUnfreezeBoundary Unfreeze 的 x 恰等于 a_i 整令移出，比 a_i 大 1 被拒。
func TestUnfreezeBoundary(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustCode(t, mg.Unfreeze("A", 0, "o1", 61), freeze.ErrUnfreezeExceeds)
	mustOK(t, mg.Unfreeze("A", 0, "o1", 60))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 100, V: 100,
		Orders: []freeze.OrderView{},
	})
}

// TestExpiredOrderNotFound 已失效令的 Unfreeze 与 Seize 报令不存在。
func TestExpiredOrderNotFound(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 5))
	mustCode(t, mg.Unfreeze("A", 5, "o1", 10), freeze.ErrOrderNotFound)
	mustCode(t, mg.Seize("A", 5, "o1", 10), freeze.ErrOrderNotFound)
}

// TestExpiredIDReuse 已失效的编号可复用（失效清理之后才判定重复）。
func TestExpiredIDReuse(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 5))
	// t=5 时旧 o1 已失效，编号可复用。
	mustOK(t, mg.Freeze("A", 5, "o1", 30, 0))
	mustQuery(t, mg, "A", 5, freeze.AccountView{
		B: 100, V: 70,
		Orders: []freeze.OrderView{ov("o1", 30, 30, 0)},
	})
	// 仍在队列中的令编号不得重复。
	mustCode(t, mg.Freeze("A", 5, "o1", 10, 0), freeze.ErrDuplicateOrder)
}

// TestLaterOrdersDoNotAffectEarlier 后序令的增减不影响前序令的有效冻结额。
func TestLaterOrdersDoNotAffectEarlier(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustOK(t, mg.Freeze("A", 0, "o1", 60, 0))
	mustOK(t, mg.Freeze("A", 0, "o2", 50, 0))
	before, err := mg.Query("A", 0)
	mustOK(t, err)

	// 追加后序令、减少后序令的名义金额，前序 e 不变。
	mustOK(t, mg.Freeze("A", 0, "o3", 90, 0))
	mustOK(t, mg.Unfreeze("A", 0, "o3", 40))
	after, err := mg.Query("A", 0)
	mustOK(t, err)
	if after.Orders[0] != before.Orders[0] || after.Orders[1] != before.Orders[1] {
		t.Fatalf("后序令变化影响了前序令: before=%+v after=%+v", before.Orders, after.Orders)
	}
	if after.Orders[0].E != 60 || after.Orders[1].E != 40 || after.Orders[2].E != 0 {
		t.Fatalf("有效冻结额不符合逐令公式: %+v", after.Orders)
	}
}

// TestTimeRegression 时序倒退被拒，且被拒绝的操作不推进 m、不改变状态。
func TestTimeRegression(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 10, 100))
	if got := mg.MaxTime(); got != 10 {
		t.Fatalf("m 应为 10，得到 %d", got)
	}
	mustCode(t, mg.Deposit("A", 9, 50), freeze.ErrTimeRegression)
	mustCode(t, mg.Debit("A", 9, 1), freeze.ErrTimeRegression)
	mustCode(t, mg.Freeze("A", 9, "o1", 1, 0), freeze.ErrTimeRegression)
	if _, err := mg.Query("A", 9); true {
		mustCode(t, err, freeze.ErrTimeRegression)
	}
	// m 未被推进，状态未变；同刻 t=10 仍可操作。
	if got := mg.MaxTime(); got != 10 {
		t.Fatalf("被拒绝的操作不应推进 m，得到 %d", got)
	}
	mustQuery(t, mg, "A", 10, freeze.AccountView{
		B: 100, V: 100,
		Orders: []freeze.OrderView{},
	})
}

// TestRejectedKeepsState 各类被拒绝的操作不改变任何状态。
func TestRejectedKeepsState(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 3, 100))
	mustOK(t, mg.Freeze("A", 3, "o1", 60, 0))
	want := freeze.AccountView{
		B: 100, V: 40,
		Orders: []freeze.OrderView{ov("o1", 60, 60, 0)},
	}
	rejections := []error{
		mg.Debit("A", 3, 41),                  // 可用不足
		mg.Freeze("A", 3, "o1", 5, 0),         // 令编号重复
		mg.Unfreeze("A", 3, "o1", 61),         // 解冻超额
		mg.Unfreeze("A", 3, "ghost", 1),       // 令不存在
		mg.Seize("A", 3, "o1", 61),            // 扣划超额
		mg.SeizeQ("A", 3, 61),                 // 扣划超额
		mg.Freeze("B", 3, "o1", 5, 0),         // 账户不存在
		mg.Freeze("A", 3, "", 5, 0),           // 参数非法
		mg.Deposit("A", 3, freeze.MaxBalance), // 存入后 B 超上限
		mg.Debit("A", 2, 1),                   // 时序倒退
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
	}
	if got := mg.MaxTime(); got != 3 {
		t.Fatalf("被拒绝的操作不应推进 m，得到 %d", got)
	}
	mustQuery(t, mg, "A", 3, want)
}

// TestErrorPrecedence 按参数非法、时序倒退、账户不存在、令编号重复或不存在、
// 超额类或可用不足的顺序只报第一个。
func TestErrorPrecedence(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 10, 100))
	mustOK(t, mg.Freeze("A", 10, "o1", 60, 0))

	// 参数非法优先于时序倒退。
	mustCode(t, mg.Debit("A", 5, 0), freeze.ErrInvalidParam)
	mustCode(t, mg.Freeze("A", 5, "x", 10, 5), freeze.ErrInvalidParam)
	// 时序倒退优先于账户不存在。
	mustCode(t, mg.Debit("ghost", 5, 1), freeze.ErrTimeRegression)
	// 账户不存在优先于令编号重复/不存在与超额。
	mustCode(t, mg.Unfreeze("ghost", 10, "o1", 1), freeze.ErrAccountNotFound)
	mustCode(t, mg.SeizeQ("ghost", 10, 1), freeze.ErrAccountNotFound)
	// 令编号重复优先于其他 Freeze 后续判定；令不存在优先于超额。
	mustCode(t, mg.Freeze("A", 10, "o1", 10, 0), freeze.ErrDuplicateOrder)
	mustCode(t, mg.Unfreeze("A", 10, "ghost", 999), freeze.ErrOrderNotFound)
	mustCode(t, mg.Seize("A", 10, "ghost", 999), freeze.ErrOrderNotFound)
	// 超额类：Unfreeze 的 x 大于 a_i、Seize 的 x 大于 e_i、Debit 的 x 大于 V。
	mustCode(t, mg.Unfreeze("A", 10, "o1", 61), freeze.ErrUnfreezeExceeds)
	mustCode(t, mg.Seize("A", 10, "o1", 61), freeze.ErrSeizeExceeds)
	mustCode(t, mg.Debit("A", 10, 41), freeze.ErrInsufficientAvail)
}

// TestInvalidParams 空编号、金额越界、t 越界、存入后 B 超上限均为参数非法。
func TestInvalidParams(t *testing.T) {
	mg := freeze.NewManager()
	mustOK(t, mg.Deposit("A", 0, 100))
	mustCode(t, mg.Deposit("", 0, 1), freeze.ErrInvalidParam)
	mustCode(t, mg.Deposit("A", -1, 1), freeze.ErrInvalidParam)
	mustCode(t, mg.Deposit("A", freeze.MaxAmount+1, 1), freeze.ErrInvalidParam)
	mustCode(t, mg.Deposit("A", 0, 0), freeze.ErrInvalidParam)
	mustCode(t, mg.Deposit("A", 0, freeze.MaxAmount+1), freeze.ErrInvalidParam)
	mustCode(t, mg.Deposit("A", 0, freeze.MaxBalance), freeze.ErrInvalidParam)
	mustCode(t, mg.Freeze("A", 0, "", 1, 0), freeze.ErrInvalidParam)
	mustCode(t, mg.Freeze("A", 0, "o1", 0, 0), freeze.ErrInvalidParam)
	mustCode(t, mg.Unfreeze("A", 0, "o1", 0), freeze.ErrInvalidParam)
	mustCode(t, mg.SeizeQ("A", 0, 0), freeze.ErrInvalidParam)
	if _, err := mg.Query("", 0); true {
		mustCode(t, err, freeze.ErrInvalidParam)
	}
	// 全部拒绝，m 仍为 0，状态不变。
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 100, V: 100,
		Orders: []freeze.OrderView{},
	})
}

// TestDepositCreatesAccount Deposit 对不存在的账户先创建；其他操作报账户不存在。
func TestDepositCreatesAccount(t *testing.T) {
	mg := freeze.NewManager()
	mustCode(t, mg.Debit("A", 0, 1), freeze.ErrAccountNotFound)
	mustCode(t, mg.Freeze("A", 0, "o1", 1, 0), freeze.ErrAccountNotFound)
	mustCode(t, mg.Unfreeze("A", 0, "o1", 1), freeze.ErrAccountNotFound)
	mustCode(t, mg.Seize("A", 0, "o1", 1), freeze.ErrAccountNotFound)
	mustCode(t, mg.SeizeQ("A", 0, 1), freeze.ErrAccountNotFound)
	if _, err := mg.Query("A", 0); true {
		mustCode(t, err, freeze.ErrAccountNotFound)
	}
	mustOK(t, mg.Deposit("A", 0, 10))
	mustQuery(t, mg, "A", 0, freeze.AccountView{
		B: 10, V: 10,
		Orders: []freeze.OrderView{},
	})
}
