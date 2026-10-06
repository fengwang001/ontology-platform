package defassign

import "testing"

func expectInputError(t *testing.T, src string, want InputErrorKind) {
	t.Helper()
	rep, err := CheckText("t", src)
	if err == nil {
		t.Fatalf("want input error %v, got report:\n%s", want, rep.Text())
	}
	ie, ok := err.(*InputError)
	if !ok {
		t.Fatalf("want *InputError, got %T: %v", err, err)
	}
	if ie.Kind != want {
		t.Fatalf("error kind = %v, want %v", ie.Kind, want)
	}
}

func TestUndeclaredVariable(t *testing.T) {
	expectInputError(t, "use q\n", ErrUndeclaredVar)
	expectInputError(t, "var x\nassign y\n", ErrUndeclaredVar)
}

func TestBreakOutsideLoop(t *testing.T) {
	expectInputError(t, "var x\nbreak\n", ErrBreakOutsideLoop)
	// break 在 try 里但不在循环里仍非法。
	expectInputError(t, "var x\ntry\n break\n cleanup\nend\n", ErrBreakOutsideLoop)
	// 循环内（含 try 清理内）合法。
	rep := mustCheck(t, "var x\nloop\n try\n cleanup\n  break\n end\nend\n")
	if len(rep.Diags) != 0 {
		t.Fatalf("unexpected diags:\n%s", rep.Text())
	}
}

func TestHandlerOutsideTry(t *testing.T) {
	expectInputError(t, "var x\nhandler\n", ErrHandlerOutsideTry)
	expectInputError(t, "var x\nif\n handler\nend\n", ErrHandlerOutsideTry)
}

func TestStructureCycle(t *testing.T) {
	p, err := Parse("cyc", "var x\nif\n use x\nelse\nend\n")
	if err != nil {
		t.Fatal(err)
	}
	// 手工制造结构环：else 侧包含 if 节点自身。
	ifn := p.Stmts[1]
	ifn.Else = append(ifn.Else, ifn)
	_, err = Check(p)
	if ie, ok := err.(*InputError); !ok || ie.Kind != ErrStructureCycle {
		t.Fatalf("want structure cycle, got %v", err)
	}
}

func TestRejectionOrdering(t *testing.T) {
	// 同时含环与未声明变量：环优先。
	p, _ := Parse("o", "var x\nif\n use y\nelse\nend\n")
	ifn := p.Stmts[1]
	ifn.Else = append(ifn.Else, ifn)
	_, err := Check(p)
	if ie, ok := err.(*InputError); !ok || ie.Kind != ErrStructureCycle {
		t.Fatalf("want cycle first, got %v", err)
	}

	// 未声明变量 + 非法 break + 孤儿 handler：未声明优先。
	expectInputError(t, "var x\nuse y\nbreak\nhandler\n", ErrUndeclaredVar)
	// 非法 break + 孤儿 handler：break 优先。
	expectInputError(t, "break\nhandler\n", ErrBreakOutsideLoop)
}

func TestNoPartialDiagnosticsOnBadInput(t *testing.T) {
	rep, err := CheckText("t", "var x\nassign y\nuse x\n")
	if err == nil {
		t.Fatalf("want error")
	}
	if rep != nil {
		t.Fatalf("bad input must not yield partial report")
	}
}
