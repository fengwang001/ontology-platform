package admission

import (
	"fmt"
	"testing"
)

// f-d 恰等于 M 可行，大 1 不可行。
func TestBoundaryLateness(t *testing.T) {
	c := NewController()
	c.Submit("a", 0, 4, 6, 0, 8)
	r := c.Submit("b", 0, 2, 4, 2, 5) // f_b=2，f_a=6，6-6=0==M
	if !r.Accepted || len(r.Evicted) != 0 {
		t.Fatalf("f-d 恰等于 M 应通过, got %+v", r)
	}

	c2 := NewController()
	c2.Submit("a", 0, 4, 5, 0, 100)
	r2 := c2.Submit("b", 0, 2, 4, 0, 1) // f_b=2<=4；f_a=6，6-5=1>0
	if r2.Accepted || r2.Reason != RejectOverload {
		t.Fatalf("f-d=M+1 应过载拒绝, got %+v", r2)
	}
	if p := c2.Pending(); len(p) != 1 || p[0].ID != "a" || p[0].Remain != 4 || p[0].Started {
		t.Fatalf("拒绝后状态应恢复, got %+v", p)
	}
}

// 题给主例：t=0 接纳 a,b；t=1 提交 c，a 已开始不可动，驱逐 b；t=10 结算 5+8。
func TestSpecExampleEviction(t *testing.T) {
	c := NewController()
	if r := c.Submit("a", 0, 4, 6, 0, 8); !r.Accepted {
		t.Fatalf("a 应接纳: %+v", r)
	}
	if r := c.Submit("b", 0, 3, 8, 0, 3); !r.Accepted {
		t.Fatalf("b 应接纳: %+v", r)
	}
	r := c.Submit("c", 1, 2, 5, 0, 5)
	if !r.Accepted || fmt.Sprint(r.Evicted) != "[b]" {
		t.Fatalf("应接纳 c 并驱逐 b, got %+v", r)
	}
	p := c.Pending()
	if fmt.Sprint(p) != fmt.Sprint([]PendingItem{{"c", 2, false}, {"a", 3, true}}) {
		t.Fatalf("pending=%+v", p)
	}
	if r2 := c.Advance(10); !r2.OK {
		t.Fatalf("advance: %+v", r2)
	}
	if c.Value() != 13 {
		t.Fatalf("value=%d 期望 13", c.Value())
	}
	if fmt.Sprint(c.Evicted()) != "[b]" {
		t.Fatalf("evicted=%v", c.Evicted())
	}
}

// 题给反例：c' 致 a 超期；先驱逐 b 仍不可行，再选中 c' 自己，拒绝，b 保留，全部恢复。
func TestSpecExampleIncomingEvicted(t *testing.T) {
	c := NewController()
	c.Submit("a", 0, 4, 6, 0, 8)
	c.Submit("b", 0, 3, 8, 0, 3)
	r := c.Submit("cp", 1, 3, 5, 0, 9) // f=4,7(超1),10
	if r.Accepted || r.Reason != RejectOverload {
		t.Fatalf("应过载拒绝, got %+v", r)
	}
	if c.Now() != 0 || c.Value() != 0 || len(c.Evicted()) != 0 {
		t.Fatalf("拒绝后未恢复: now=%d value=%d evicted=%v", c.Now(), c.Value(), c.Evicted())
	}
	p := c.Pending()
	if fmt.Sprint(p) != fmt.Sprint([]PendingItem{{"a", 4, false}, {"b", 3, false}}) {
		t.Fatalf("pending=%+v", p)
	}
}

// 已开始作业不可驱逐。
func TestStartedCannotEvict(t *testing.T) {
	c := NewController()
	c.Submit("a", 0, 3, 3, 0, 100)
	if r := c.Advance(1); !r.OK {
		t.Fatalf("advance: %+v", r)
	}
	p0 := c.Pending()
	if len(p0) != 1 || !p0[0].Started || p0[0].Remain != 2 {
		t.Fatalf("推进后 a 应已开始且剩 2, got %+v", p0)
	}
	// b：1+5=6 > d+M=4，独占处理器也无法完成 -> 在过载检查之前拒绝。
	r := c.Submit("b", 1, 5, 4, 0, 1)
	if r.Accepted || r.Reason != RejectInfeasible {
		t.Fatalf("应按无法完成拒绝, got %+v", r)
	}
	if p := c.Pending(); fmt.Sprint(p) != fmt.Sprint(p0) {
		t.Fatalf("恢复后 pending=%+v 期望 %+v", p, p0)
	}
	if c.Value() != 0 || len(c.Evicted()) != 0 {
		t.Fatalf("拒绝不得结算或记驱逐")
	}

	// 真正的过载场景：唯一可驱逐的未开始者是新作业自己。
	c2 := NewController()
	c2.Submit("a", 0, 3, 3, 0, 100)
	c2.Advance(1) // a 剩 2 已开始
	c3 := NewController()
	c3.Submit("a", 0, 3, 3, 0, 100)
	c3.Advance(1)
	// b 可独占完成（1+2<=3），但排到已开始的 a 之后 f=5，5-3=2>0；
	// a 已开始不能动，未开始者仅 b -> b 自己被选中 -> 过载拒绝。
	r3 := c3.Submit("b", 1, 2, 3, 0, 1)
	if r3.Accepted || r3.Reason != RejectOverload {
		t.Fatalf("已开始 a 不可驱逐，b 应过载拒绝, got %+v", r3)
	}
	if p := c3.Pending(); len(p) != 1 || p[0].ID != "a" || !p[0].Started || p[0].Remain != 2 {
		t.Fatalf("恢复后 pending=%+v", p)
	}
}

// 价值密度交叉相乘：乘积约 1e12 不溢出，密度次序不被 v 大小误导。
func TestDensityCrossMultiply(t *testing.T) {
	c := NewController()
	// 999998*1000000=999998000000 < 999999*999999=999998000001
	c.Submit("x", 0, 1_000_000, 2_000_000, 0, 999_999)
	c.Submit("y", 0, 999_999, 2_000_000, 0, 999_998)
	// f_z=2，f_x=1000002，f_y=2000001>2000000 超期 1；z 密度 500000 最大。
	r := c.Submit("z", 0, 2, 2, 0, 1_000_000)
	if !r.Accepted || fmt.Sprint(r.Evicted) != "[y]" {
		t.Fatalf("交叉相乘比较后期望先驱逐 y, got %+v pending=%+v", r, c.Pending())
	}
}

// 密度并列取编号字节序大者先出，连续驱逐多个才可行。
func TestDensityTieLargerIDFirst(t *testing.T) {
	c := NewController()
	c.Submit("aa", 0, 4, 6, 0, 100)     // 密度 25
	c.Submit("bb", 0, 2, 8, 0, 50)      // 密度 25，编号更大；初始 f_aa=4<=6,f_bb=6<=8
	c.Submit("x", 0, 2, 2, 0, 1000)     // 密度 500，f_x=2 准时
	r := c.Submit("z", 0, 2, 4, 0, 100) // 密度 50，f_z=4 准时；aa f=8 超期，bb f=10 超期
	// aa、bb 密度并列最小，bb 编号大先出；aa 仍 f=6 超期，再出；x,z 保留。
	if !r.Accepted || fmt.Sprint(r.Evicted) != "[bb aa]" {
		t.Fatalf("期望按 [bb aa] 驱逐, got %+v", r)
	}
}

// 驱逐多个后才可行；并断言非导出计数器上界。
func TestEvictMultiple(t *testing.T) {
	c := NewController()
	c.Submit("a", 0, 2, 100, 0, 1)
	c.Submit("b", 0, 2, 100, 0, 1)
	c.Submit("d", 0, 2, 100, 0, 1)
	// z(C=99,d=99)：f_z=99 准时；之后 a=101(超1)、b=103、d=105。
	r := c.Submit("z", 0, 99, 99, 0, 99)
	// 三者密度并列 1/2，按编号大先出：d、b、a；驱逐 a 后 z 单独准时。
	if !r.Accepted || fmt.Sprint(r.Evicted) != "[d b a]" {
		t.Fatalf("期望驱逐 [d b a], got %+v", r)
	}
	p := c.Pending()
	if len(p) != 1 || p[0].ID != "z" || p[0].Remain != 99 {
		t.Fatalf("pending=%+v", p)
	}
	sorts, scans, attempts := c.lastSubmitCounters()
	if sorts > 1 {
		t.Fatalf("排序次数 %d > 1", sorts)
	}
	if scans != len(r.Evicted)+1 {
		t.Fatalf("判定次数 %d 期望 %d", scans, len(r.Evicted)+1)
	}
	if attempts != 3 {
		t.Fatalf("驱逐尝试次数 %d 期望 3", attempts)
	}
}

// 延迟结算向下取整；容忍边界；准时全额。
func TestDelayDiscountFloor(t *testing.T) {
	c := NewController()
	if r := c.Submit("p", 0, 5, 3, 3, 8); !r.Accepted {
		t.Fatalf("应接纳: %+v", r)
	}
	if r := c.Advance(5); !r.OK || c.Value() != 4 {
		t.Fatalf("结算价值=%d 期望 4", c.Value())
	}

	c2 := NewController()
	c2.Submit("q", 0, 5, 2, 3, 8) // f-d=3，floor(8*1/4)=2
	c2.Advance(5)
	if c2.Value() != 2 {
		t.Fatalf("容忍边界价值=%d 期望 2", c2.Value())
	}

	c3 := NewController()
	c3.Submit("r", 0, 5, 5, 3, 8)
	c3.Advance(5)
	if c3.Value() != 8 {
		t.Fatalf("准时价值=%d 期望 8", c3.Value())
	}
}

// 推进中恰在 d 完成，准时结算全额。
func TestFinishExactlyAtDeadline(t *testing.T) {
	c := NewController()
	c.Submit("j", 0, 4, 4, 0, 7)
	c.Advance(2)
	if p := c.Pending(); len(p) != 1 || p[0].Remain != 2 || !p[0].Started {
		t.Fatalf("推进 2 后应已开始剩 2, got %+v", p)
	}
	if c.Now() != 2 || c.Value() != 0 {
		t.Fatalf("中途不应结算")
	}
	c.Advance(4)
	if c.Now() != 4 || c.Value() != 7 || len(c.Pending()) != 0 {
		t.Fatalf("now=%d value=%d pending=%+v", c.Now(), c.Value(), c.Pending())
	}
}

// 编号复用：待处理期间重复被拒；完成后编号可复用。
func TestIDReuseAndDuplicate(t *testing.T) {
	c := NewController()
	if r := c.Submit("a", 0, 2, 2, 0, 5); !r.Accepted {
		t.Fatalf("%+v", r)
	}
	if r := c.Submit("a", 0, 2, 4, 0, 5); r.Accepted || r.Reason != RejectDuplicateID {
		t.Fatalf("待处理期间应按重复拒绝, got %+v", r)
	}
	c.Advance(2)
	if r := c.Submit("a", 2, 1, 3, 0, 9); !r.Accepted {
		t.Fatalf("完成后编号应可复用, got %+v", r)
	}
}

// 被拒时推进不落盘：时钟、价值、待处理均不变，由后续成功操作补做。
func TestRejectedSubmitRollsBackAdvance(t *testing.T) {
	c := NewController()
	c.Submit("q", 0, 3, 3, 0, 7)
	r := c.Submit("z", 5, 10, 6, 0, 1) // 5+10>6+0 无法完成
	if r.Accepted || r.Reason != RejectInfeasible {
		t.Fatalf("应按无法完成拒绝, got %+v", r)
	}
	if c.Now() != 0 || c.Value() != 0 || len(c.Pending()) != 1 {
		t.Fatalf("拒绝不得落盘推进: now=%d value=%d pending=%+v", c.Now(), c.Value(), c.Pending())
	}
	if r2 := c.Advance(5); !r2.OK || c.Now() != 5 || c.Value() != 7 || len(c.Pending()) != 0 {
		t.Fatalf("补做结算失败: %+v now=%d value=%d pending=%+v", r2, c.Now(), c.Value(), c.Pending())
	}
}

// 拒绝原因优先级与时钟回退。
func TestRejectReasonsAndClockBack(t *testing.T) {
	c := NewController()
	if r := c.Submit("", 0, 1, 1, 0, 1); r.Reason != RejectInvalidArgs {
		t.Fatalf("空编号应非法, got %+v", r)
	}
	long33 := "123456789012345678901234567890123"
	if r := c.Submit(long33, 0, 1, 1, 0, 1); r.Reason != RejectInvalidArgs {
		t.Fatalf("33 字节编号应非法, got %+v", r)
	}
	if r := c.Submit("x", 0, 0, 1, 0, 1); r.Reason != RejectInvalidArgs {
		t.Fatalf("C=0 应非法, got %+v", r)
	}
	if r := c.Submit("x", 0, 1, 1, 1_000_001, 1); r.Reason != RejectInvalidArgs {
		t.Fatalf("M 越界应非法, got %+v", r)
	}
	if r := c.Advance(-1); r.OK || r.Reason != RejectInvalidArgs {
		t.Fatalf("负时钟应非法, got %+v", r)
	}
	if r := c.Advance(-1); r.OK || r.Reason != RejectInvalidArgs {
		t.Fatalf("时钟回退应拒绝, got %+v", r)
	}
	// 参数非法优先于时钟回退判断。
	if r := c.Submit("", -1, 0, -1, -1, 0); r.Reason != RejectInvalidArgs {
		t.Fatalf("应只报参数非法, got %+v", r)
	}
}
