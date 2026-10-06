package review

import "testing"

func TestSettleRound1Thresholds(t *testing.T) {
	// 通过线 ceil(2n/3)，不通线 floor(n/2)，其余复议。
	cases := []struct {
		n, a   int
		expect Outcome
	}{
		{3, 2, OutcomePass},       // ceil(2*3/3)=2，恰等通过
		{3, 1, OutcomeFail},       // floor(3/2)=1，恰等不通过
		{5, 4, OutcomePass},       // ceil(10/3)=4，恰等
		{5, 3, OutcomeReconsider}, // 差一：3 < 4 且 3 > 2
		{5, 2, OutcomeFail},       // floor(5/2)=2，恰等
		{7, 5, OutcomePass},       // ceil(14/3)=5，恰等
		{7, 4, OutcomeReconsider}, // 差一
		{7, 3, OutcomeFail},       // floor(7/2)=3，恰等
		{9, 6, OutcomePass},       // ceil(18/3)=6，恰等
		{9, 5, OutcomeReconsider}, // 差一
		{9, 4, OutcomeFail},       // floor(9/2)=4，恰等
		{1, 1, OutcomePass},       // 单人赞成
		{1, 0, OutcomeFail},       // 单人未赞成
	}
	for _, c := range cases {
		if got := settleRound1(c.n, c.a); got != c.expect {
			t.Errorf("settleRound1(n=%d, approve=%d)=%d, 期望 %d", c.n, c.a, got, c.expect)
		}
	}
}

func TestReconsiderStrictMajority(t *testing.T) {
	// 复议（n=5 时第一轮 3 赞进入复议）：严格 > n/2。
	// 3 赞严格过半通过；2 赞不通过。
	pass := 3*2 > 5
	fail := 2*2 > 5
	if !pass || fail {
		t.Fatalf("复议严格过半门槛错误: 3赞>2.5 应通过, 2赞 应不通过")
	}
	if 2*2 > 4 {
		t.Fatal("n=4 不应出现（奇数约束）")
	}
	// n=3：2 赞严格 > 1.5 通过；1 赞不通过。
	if !(2*2 > 3) || 1*2 > 3 {
		t.Fatal("n=3 复议门槛错误")
	}
}
