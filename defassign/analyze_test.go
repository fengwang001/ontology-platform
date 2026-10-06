package defassign

import (
	"strings"
	"testing"
)

func diagKeys(rep *Report) []string {
	ks := make([]string, len(rep.Diags))
	for i, d := range rep.Diags {
		ks[i] = d.Kind.String() + "@" + itoa(int(d.At)) + ":" + d.Var
	}
	return ks
}

func mustCheck(t *testing.T, src string) *Report {
	t.Helper()
	rep, err := CheckText("t", src)
	if err != nil {
		t.Fatalf("unexpected input error: %v\ninput:\n%s", err, src)
	}
	return rep
}

func expectKeys(t *testing.T, rep *Report, want []string) {
	t.Helper()
	got := diagKeys(rep)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("diags = %v, want %v\n%s", got, want, rep.Text())
	}
}

func TestBasicAndBranchMerge(t *testing.T) {
	rep := mustCheck(t, `
var x y
use x
assign y
use y
`)
	expectKeys(t, rep, []string{"maybe-unassigned@2:x"})

	rep = mustCheck(t, `
var a b
if
 assign a
 assign b
else
 assign b
end
use a
use b
`)
	expectKeys(t, rep, []string{"maybe-unassigned@6:a"})
}

func TestConstantConditionPrunesSide(t *testing.T) {
	rep := mustCheck(t, `
var a
iftrue
 assign a
else
 use a
end
use a
`)
	expectKeys(t, rep, nil)

	rep = mustCheck(t, `
var a
iffalse
 assign a
end
use a
`)
	expectKeys(t, rep, []string{"maybe-unassigned@4:a"})
}

func TestLoopZeroAndAtLeastOnce(t *testing.T) {
	rep := mustCheck(t, `
var x
loop
 assign x
end
use x
`)
	expectKeys(t, rep, []string{"maybe-unassigned@4:x"})

	rep = mustCheck(t, `
var x
loop1
 assign x
end
use x
`)
	expectKeys(t, rep, nil)
}

func TestLoopBodyCannotRelyOnPreviousIteration(t *testing.T) {
	rep := mustCheck(t, `
var x
loop
 use x
 assign x
end
`)
	// 读取依赖不到上一轮末尾赋值 => 可能未赋值；该读取发生时变量恒未赋值、
	// 读取实际不执行，故末尾赋值从未被读取 => 无效赋值。两类诊断并存。
	expectKeys(t, rep, []string{"maybe-unassigned@3:x", "dead-assign@4:x"})

	rep = mustCheck(t, `
var x
loop
 assign x
 use x
end
`)
	expectKeys(t, rep, nil)
}

func TestBreakDoesNotJoinBodyMerge(t *testing.T) {
	rep := mustCheck(t, `
var x
loop1
 assign x
 break
end
use x
`)
	expectKeys(t, rep, nil)

	rep = mustCheck(t, `
var x
loop
 assign x
 break
end
use x
`)
	expectKeys(t, rep, []string{"maybe-unassigned@5:x"})

	rep = mustCheck(t, `
var x
loop1
 if
  break
 else
 end
 use x
 assign x
end
`)
	expectKeys(t, rep, []string{"maybe-unassigned@5:x", "dead-assign@6:x"})
}

func TestHandlerUsesPreBodyState(t *testing.T) {
	rep := mustCheck(t, `
var x
try
 assign x
 handler
  use x
 cleanup
end
`)
	// 分支读取按进入前状态判未赋值；被保护体正常结束车道无读取即离开，
	// 分支读取时该赋值尚未发生 => 该赋值从未被读取 => 无效。
	expectKeys(t, rep, []string{"dead-assign@3:x", "maybe-unassigned@5:x"})

	rep = mustCheck(t, `
var x
assign x
try
 handler
  use x
 cleanup
  use x
end
`)
	expectKeys(t, rep, nil)
}

func TestCleanupAssignReachesAfter(t *testing.T) {
	rep := mustCheck(t, `
var x
try
 cleanup
  assign x
end
use x
`)
	expectKeys(t, rep, nil)
}

func TestBodyEndAssignSkippedByMidThrow(t *testing.T) {
	rep := mustCheck(t, `
var x
try
 assign x
end
use x
`)
	expectKeys(t, rep, []string{"maybe-unassigned@4:x"})

	rep = mustCheck(t, `
var x
try
 assign x
 handler
 cleanup
end
use x
`)
	expectKeys(t, rep, []string{"maybe-unassigned@5:x"})
}

func TestBreakInCleanupMakesAfterUnreachable(t *testing.T) {
	rep := mustCheck(t, `
var x y
loop
 try
  cleanup
   break
 end
 use y
 assign x
end
`)
	// 清理恒以跳出结束：use y / assign x 不可达；不可达代码无任何诊断。
	expectKeys(t, rep, nil)
}

func TestDeadAssignAndLoopBackEdge(t *testing.T) {
	rep := mustCheck(t, `
var x
assign x
`)
	expectKeys(t, rep, []string{"dead-assign@2:x"})

	rep = mustCheck(t, `
var x
assign x
if
 use x
else
end
`)
	expectKeys(t, rep, nil)

	rep = mustCheck(t, `
var x
loop1
 assign x
 use x
end
`)
	expectKeys(t, rep, nil)

	rep = mustCheck(t, `
var x
assign x
assign x
use x
`)
	expectKeys(t, rep, []string{"dead-assign@2:x"})
}

func TestDeadAndMaybeUnassignedDoNotCancel(t *testing.T) {
	rep := mustCheck(t, `
var x
if
 assign x
else
end
use x
assign x
`)
	expectKeys(t, rep, []string{"maybe-unassigned@4:x", "dead-assign@5:x"})
}

func TestReturnRunsCleanup(t *testing.T) {
	rep := mustCheck(t, `
var x
try
 return
 cleanup
  assign x
end
use x
`)
	// return 经清理离开函数；结构后代码不可达；清理中赋值后无读取 => 无效。
	expectKeys(t, rep, []string{"dead-assign@4:x"})
}

func TestWitnessPaths(t *testing.T) {
	rep := mustCheck(t, `
var x
if
 use x
else
 use x
end
`)
	if len(rep.Diags) != 2 {
		t.Fatalf("want 2 diagnostics (one per side), got:\n%s", rep.Text())
	}
	for i, want := range []string{traceRoot + ">" + tagIfThen, traceRoot + ">" + tagIfElse} {
		if len(rep.Diags[i].Witness) != 1 || rep.Diags[i].Witness[0] != want {
			t.Fatalf("witness %d = %v, want %s", i, rep.Diags[i].Witness, want)
		}
	}
}
