package ontology

import "testing"

// TestReplayLog 用一条确定序列逐条打印输入、输出与判定依据，演示可精确复现。
func TestReplayLog(t *testing.T) {
	type reg struct {
		id             string
		bid, q, budget int64
	}
	regs := []reg{
		{"A", 50, 20, 1_000_000},
		{"B", 40, 30, 1_000_000},
		{"C", 45, 10, 1_000_000},
		{"D", 8, 50, 1_000_000},
		{"E", 60, 5, 50},
	}
	e := NewEngine()
	for _, r := range regs {
		err := e.Register(r.id, r.bid, r.q, r.budget)
		t.Logf("输入 Register(%q,bid=%d,q=%d,budget=%d) => 输出 err=%v；判定: 参数在界内且 id 唯一 => 受理",
			r.id, r.bid, r.q, r.budget, err)
	}

	ws, err := e.Auction(2, 10)
	t.Logf("输入 Auction(K=2,P=10) => 输出 winners=%+v err=%v；判定: D(bid<P)、E(a=50<bid=60)出局；s 序 B(1200)>A(1000)>C(450)；p_B=min(40,floor(1000/30)+1=34)=34, p_A=min(50,floor(450/20)+1=23)=23",
		ws, err)

	err = e.Resolve(1, []string{"B"})
	t.Logf("输入 Resolve(拍卖号=1, 点击={B}) => 输出 err=%v；判定: B 点击扣34并 f=0、q=30+floor(970/8)=151；A 未点击仅释放占用、f=1、q=20-ceil(20/16)=18", err)

	b, _ := e.GetBidder("B")
	a, _ := e.GetBidder("A")
	t.Logf("终态查询 => B=%+v A=%+v；判定: 已冻结的拍卖价格不随结算改变，之后拍卖以新 q/f 重算 qe", b, a)

	// 拒绝路径也打印。
	err = e.Register("A", 1, 1, 1)
	t.Logf("输入重复 Register(A,...) => 输出 err=%v；判定: 参数合法但 id 已存在 => ErrBidderExists", err)
	err = e.Resolve(1, []string{"B"})
	t.Logf("输入重复 Resolve(1) => 输出 err=%v；判定: 拍卖 1 已结算 => ErrAuctionResolved，状态不变", err)
}
