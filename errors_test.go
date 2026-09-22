package ontology

import (
	"errors"
	"strings"
	"testing"
)

var syntaxKinds = []error{ErrEmptyExpr, ErrIllegalChar, ErrBadLiteral, ErrUnmatchedParen}

// checkErr 断言错误属于 want 类别、不属于其他类别，且字节位置正确。
func checkErr(t *testing.T, name, in string, want error, wantPos int) {
	t.Helper()
	_, err := Eval(in)
	if err == nil {
		t.Fatalf("%s: Eval(%q) 应当报错 %v", name, in, want)
	}
	if !errors.Is(err, want) {
		t.Errorf("%s: Eval(%q) err = %v, 期望类别 %v", name, in, err, want)
	}
	for _, k := range syntaxKinds {
		if k != want && errors.Is(err, k) {
			t.Errorf("%s: Eval(%q) err = %v, 不应属于类别 %v", name, in, err, k)
		}
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%s: Eval(%q) err 类型为 %T, 期望 *Error", name, in, err)
	}
	if wantPos >= 0 && e.Pos != wantPos {
		t.Errorf("%s: Eval(%q) 位置 = %d, 期望 %d", name, in, e.Pos, wantPos)
	}
}

func TestErrEmptyExpr(t *testing.T) {
	checkErr(t, "空串", "", ErrEmptyExpr, -1)
	checkErr(t, "纯空白", " \t\n\r ", ErrEmptyExpr, -1)
}

func TestErrIllegalChar(t *testing.T) {
	checkErr(t, "非法字符", "1+2@", ErrIllegalChar, 3)
	checkErr(t, "字母", "abc", ErrIllegalChar, 0)
	checkErr(t, "隐式乘法", "2(3)", ErrIllegalChar, 1)
	checkErr(t, "括号相邻", "(1)(2)", ErrIllegalChar, 3)
	checkErr(t, "数字相邻", "2 3", ErrIllegalChar, 2)
	checkErr(t, "意外结束", "1+", ErrIllegalChar, 2)
	checkErr(t, "中文", "1+甲", ErrIllegalChar, 2)
}

func TestErrBadLiteral(t *testing.T) {
	checkErr(t, "两个小数点", "1.2.3", ErrBadLiteral, 0)
	checkErr(t, "点前移", "..5", ErrBadLiteral, 0)
	checkErr(t, "连续点", "1..2", ErrBadLiteral, 0)
	checkErr(t, "带偏移", "3 + 1.2.3", ErrBadLiteral, 4)
	checkErr(t, "只有点", ".", ErrBadLiteral, 0)
}

func TestErrUnmatchedParen(t *testing.T) {
	checkErr(t, "开括号未闭合", "(1+2", ErrUnmatchedParen, 0)
	checkErr(t, "多余右括号", "1+2)", ErrUnmatchedParen, 3)
	checkErr(t, "嵌套外层未闭合", "((1+2)", ErrUnmatchedParen, 0)
	checkErr(t, "嵌套内层未闭合", "(1+(2", ErrUnmatchedParen, 3)
}

// TestUnmatchedParenOrdinal 报错信息必须指出是第几个开括号未闭合。
func TestUnmatchedParenOrdinal(t *testing.T) {
	cases := []struct {
		in      string
		ordinal string
	}{
		{"(1+2", "第 1 个开括号未闭合"},
		{"((1+2)", "第 1 个开括号未闭合"},
		{"(1+(2", "第 2 个开括号未闭合"},
		{"(1)+((2)", "第 2 个开括号未闭合"},
	}
	for _, c := range cases {
		_, err := Eval(c.in)
		if !errors.Is(err, ErrUnmatchedParen) {
			t.Errorf("Eval(%q) err = %v, 期望 ErrUnmatchedParen", c.in, err)
			continue
		}
		if !strings.Contains(err.Error(), c.ordinal) {
			t.Errorf("Eval(%q) 错误信息 %q 不含 %q", c.in, err.Error(), c.ordinal)
		}
	}
}
