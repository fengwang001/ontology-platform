package mask

import (
	"errors"
	"fmt"
	"testing"
)

// TestMaskRules 钉住不变量 1：对任意 (role, column, raw)，输出逐字符等于规则。
func TestMaskRules(t *testing.T) {
	cases := []struct {
		role, column, raw, want string
	}{
		// admin 原样
		{"admin", "name", "张三", "张三"},
		{"admin", "card", "1234567890123456", "1234567890123456"},
		{"admin", "phone", "13800138000", "13800138000"},
		// none 全空
		{"none", "name", "张三", ""},
		{"none", "card", "1234567890123456", ""},
		{"none", "phone", "13800138000", ""},
		// analyst.name：保留首字符
		{"analyst", "name", "", ""},
		{"analyst", "name", "张", "张"},
		{"analyst", "name", "李四", "李*"},
		{"analyst", "name", "欧阳娜娜", "欧***"},
		// analyst.card：保留末 4，<4 全掩
		{"analyst", "card", "", ""},
		{"analyst", "card", "9", "*"},
		{"analyst", "card", "999", "***"},
		{"analyst", "card", "1234", "1234"},
		{"analyst", "card", "12345", "*2345"},
		{"analyst", "card", "1234567890123456", "************3456"},
		// analyst.phone：保留末 4，<4 全掩
		{"analyst", "phone", "", ""},
		{"analyst", "phone", "100", "***"},
		{"analyst", "phone", "10086", "*0086"},
		{"analyst", "phone", "13800138000", "*******8000"},
	}
	for _, c := range cases {
		got1, err1 := MaskColumn(c.role, c.column, c.raw)
		got2, err2 := MaskColumn(c.role, c.column, c.raw)
		if err1 != nil || err2 != nil {
			t.Fatalf("MaskColumn(%q,%q,%q) err: %v %v", c.role, c.column, c.raw, err1, err2)
		}
		if got1 != c.want {
			t.Errorf("MaskColumn(%q,%q,%q) = %q, want %q", c.role, c.column, c.raw, got1, c.want)
		}
		if got1 != got2 {
			t.Errorf("MaskColumn(%q,%q,%q) 不确定: %q vs %q", c.role, c.column, c.raw, got1, got2)
		}
	}
}

// TestRuleLookupConstant 钉住复杂度约束：注册 m 条规则后，
// MaskColumn 为解析 (role, column) 检查的规则条数不随 m 线性增长。
func TestRuleLookupConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		for i := 0; i < m; i++ {
			RegisterRule(fmt.Sprintf("role-%d-%d", m, i), fmt.Sprintf("col-%d", i),
				func(raw string) string { return raw })
		}
		if _, err := MaskColumn(fmt.Sprintf("role-%d-%d", m, m-1), fmt.Sprintf("col-%d", m-1), "x"); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got := lastChecked.Load(); got > 3 {
			t.Fatalf("m=%d: 检查了 %d 条规则，疑似线性扫描", m, got)
		}
	}
}

// TestMaskErrorsDistinct 未知角色与未知列名是可判定且互不相同的错误。
func TestMaskErrorsDistinct(t *testing.T) {
	_, eRole := MaskColumn("root", "name", "x")
	_, eCol := MaskColumn("admin", "ssn", "x")
	if !errors.Is(eRole, ErrUnknownRole) {
		t.Errorf("未知角色应得 ErrUnknownRole, got %v", eRole)
	}
	if !errors.Is(eCol, ErrUnknownColumn) {
		t.Errorf("未知列名应得 ErrUnknownColumn, got %v", eCol)
	}
	if eRole == eCol {
		t.Error("两类错误必须互不相同")
	}
}
