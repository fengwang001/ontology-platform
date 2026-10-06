package defassign

import (
	"strings"
	"testing"
)

// buildPaddedProgram 构造：N 个互不相关变量，每个先赋值；末尾一个固定的
// 汇合结构上对变量 target 做一次读取。增大 N 只增大程序总长与无关变量数，
// 不增大到达末尾读取点的车道数。
func buildPaddedProgram(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(" v" + padVar(i))
	}
	// 结构：
	//   var v0..vN-1 target
	//   assign v0..vN-1
	//   if
	//     assign target
	//   else
	//     assign target
	//   end
	//   use target          <- 读取点：恰好两个汇合车道，两侧都赋值
	b2 := strings.Builder{}
	b2.WriteString("var")
	b2.WriteString(b.String())
	b2.WriteString(" target\n")
	for i := 0; i < n; i++ {
		b2.WriteString("assign v" + padVar(i) + "\n")
	}
	b2.WriteString("if\n assign target\nelse\n assign target\nend\nuse target\n")
	return b2.String()
}

// padVar 与 buildPaddedProgram 的声明名（v0..v15 等宽）对应。
func padVar(i int) string {
	s := itoa(i)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// TestReadPointCostIndependentOfProgramLength：单个读取点被检查的车道数
// 不随程序总长 / 无关变量数量增长，只与到达该点的汇合路径数相关。
func TestReadPointCostIndependentOfProgramLength(t *testing.T) {
	var baseline int
	measured := map[int]int{}
	for _, n := range []int{1, 4, 16, 64} {
		p, err := Parse("p", buildPaddedProgram(n))
		if err != nil {
			t.Fatal(err)
		}
		cr, err := CheckDetailed(p)
		if err != nil {
			t.Fatal(err)
		}
		// 找到末尾 use target 的位置。
		var usePos Pos
		for _, s := range p.Stmts {
			if s.Kind == KUse && s.Var == "target" {
				usePos = s.Pos
			}
		}
		hits := cr.ReadLaneHits[usePos]
		measured[n] = hits
		if n == 1 {
			baseline = hits
		}
		if hits != baseline {
			t.Fatalf("read-point lane hits grew with program size: n=%d hits=%d baseline=%d",
				n, hits, baseline)
		}
	}
	if baseline != 2 {
		t.Fatalf("expected exactly 2 merge lanes at the read, got %d", baseline)
	}
}

// TestMergeCostIgnoresUnrelatedVariables：同形汇合在变量数不同的程序上，
// mergeTouches 的「与变量相关部分」不变（车道条目数相同）；
// 指纹/集合比较只遍历车道上实际出现的变量。
func TestMergeCostIgnoresUnrelatedVariables(t *testing.T) {
	countLaneEntries := func(n int) int {
		p, _ := Parse("p", buildPaddedProgram(n))
		cr, err := CheckDetailed(p)
		if err != nil {
			t.Fatal(err)
		}
		return cr.MergeOperands
	}
	// mergeTouches 是车道条目总数；随无关变量数至多线性（线性来自对这些
	// 变量赋值语句的顺序传播，每个赋值本身是一次 O(1) 车道更新），关键是
	// 汇合点本身的比较宽度（fingerprint）不扫全部变量——这里验证条目计数
	// 不随变量数二次增长：count(64) < 16 * count(16)。
	c1 := countLaneEntries(1)
	c16 := countLaneEntries(16)
	c64 := countLaneEntries(64)
	if c64-c16 > 8*(c16-c1) {
		t.Fatalf("merge work grows super-linearly with variable count: %d %d %d", c1, c16, c64)
	}
}
