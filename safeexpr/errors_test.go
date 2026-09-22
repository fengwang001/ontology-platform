package safeexpr

import (
	"errors"
	"testing"
)

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		expr string
		kind error
		pos  int
	}{
		{"空表达式", "", ErrEmpty, -1},
		{"纯空白", "   \t\n ", ErrEmpty, -1},
		{"非法字母", "1+a", ErrIllegalChar, 2},
		{"非法感叹号", "2 ! 3", ErrIllegalChar, 2},
		{"隐式乘法", "2(3)", ErrIllegalChar, 1},
		{"中文括号", "1+（2）", ErrIllegalChar, 2},
		{"多小数点", "1.2.3", ErrMalformedNumber, 3},
		{"前导点", "..5", ErrMalformedNumber, 0},
		{"末尾点", "5.", ErrMalformedNumber, 1},
		{"未闭合括号", "(1+2", ErrUnmatchedParen, 0},
		{"嵌套未闭合", "((1+2", ErrUnmatchedParen, 1},
		{"多余闭括号", "1+2)", ErrUnmatchedParen, 3},
		{"缺操作数", "1+", ErrSyntax, 2},
		{"双运算符", "1**2", ErrSyntax, 2},
	}
	for _, c := range cases {
		_, err := Eval(c.expr)
		if err == nil {
			t.Errorf("[%s] Eval(%q) 应失败", c.name, c.expr)
			continue
		}
		if !errors.Is(err, c.kind) {
			t.Errorf("[%s] Eval(%q) 类别 = %v，期望 %v", c.name, c.expr, err, c.kind)
			continue
		}
		pe, ok := err.(*Error)
		if !ok {
			t.Errorf("[%s] 错误不是 *Error 类型", c.name)
			continue
		}
		if pe.Pos != c.pos {
			t.Errorf("[%s] 位置 = %d，期望 %d", c.name, pe.Pos, c.pos)
		}
	}
}

func TestErrorCategoriesDistinct(t *testing.T) {
	kinds := []error{
		ErrEmpty,
		ErrIllegalChar,
		ErrMalformedNumber,
		ErrUnmatchedParen,
		ErrSyntax,
		ErrDivisionByZero,
		ErrOverflow,
		ErrInexact,
	}
	for i := range kinds {
		for j := i + 1; j < len(kinds); j++ {
			if errors.Is(kinds[i], kinds[j]) || errors.Is(kinds[j], kinds[i]) {
				t.Fatalf("错误类别不唯一: %v 与 %v", kinds[i], kinds[j])
			}
		}
	}
}

func TestUnmatchedOpenIndex(t *testing.T) {
	// "((1+2" 第 2 个（最内层）开括号未闭合。
	_, err := Parse("((1+2")
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误不是 *Error: %v", err)
	}
	if !errors.Is(err, ErrUnmatchedParen) {
		t.Fatalf("类别错误: %v", err)
	}
	if pe.OpenIndex != 2 {
		t.Fatalf("未闭合开括号序号 = %d，期望 2", pe.OpenIndex)
	}
	if pe.Pos != 1 {
		t.Fatalf("未闭合开括号位置 = %d，期望 1", pe.Pos)
	}

	// "(1+(2))" 全部闭合，不应报错。
	if _, err := Parse("(1+(2))"); err != nil {
		t.Fatalf("合法表达式报错: %v", err)
	}
}

func TestIllegalCharIsBytePosition(t *testing.T) {
	// 多字节空白后定位仍必须按字节计。
	expr := "\t\n 1 @ 2"
	_, err := Eval(expr)
	pe, ok := err.(*Error)
	if !ok || !errors.Is(err, ErrIllegalChar) {
		t.Fatalf("期望非法字符错误，得到 %v", err)
	}
	if expr[pe.Pos] != '@' {
		t.Fatalf("位置 %d 指向 %q，期望 '@'", pe.Pos, string(expr[pe.Pos]))
	}
}
