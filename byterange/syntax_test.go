package byterange

import (
	"errors"
	"reflect"
	"testing"
)

// 语义 6：语法错误一律 ErrMalformed 且返回 nil 切片。
func TestParseMalformed(t *testing.T) {
	headers := []string{
		"items=0-499",    // 单位不是 bytes
		"bytes0-499",     // 缺少等号
		"bytes=500-100",  // Start > End
		"bytes=abc-def",  // 非数字
		"bytes=0-abc",    // 非数字
		"bytes=-",        // 既无起点也无后缀长度
		"bytes=",         // 空区间列表
		"bytes=0-1,,2-3", // 空区间项
		"bytes=-5-10",    // 多余的分隔符
		"",               // 空头
		"bytes=1-2-3",    // 多余的分隔符
	}
	for _, h := range headers {
		got, err := Parse(h, 1000)
		if !errors.Is(err, ErrMalformed) || got != nil {
			t.Errorf("Parse(%q) = %v, %v; want nil, ErrMalformed", h, got, err)
		}
	}
	// 语法合法但不可满足必须报 ErrUnsatisfiable，不能混为 ErrMalformed。
	if _, err := Parse("bytes=1000-", 1000); errors.Is(err, ErrMalformed) {
		t.Error("unsatisfiable reported as malformed")
	}
}

// 语义 7：数值超过 int64 报 ErrMalformed，不得溢出回绕。
func TestParseOverflow(t *testing.T) {
	headers := []string{
		"bytes=9223372036854775808-",  // MaxInt64+1
		"bytes=0-9223372036854775808", // MaxInt64+1
		"bytes=-99999999999999999999", // 远超 int64
		"bytes=18446744073709551616-", // 超过 uint64
	}
	for _, h := range headers {
		if got, err := Parse(h, 1000); !errors.Is(err, ErrMalformed) || got != nil {
			t.Errorf("Parse(%q) = %v, %v; want nil, ErrMalformed", h, got, err)
		}
	}
	// int64 边界值本身合法：起点越界 → 不可满足而非语法错误。
	if _, err := Parse("bytes=9223372036854775807-", 1000); !errors.Is(err, ErrUnsatisfiable) {
		t.Errorf("maxint start: err = %v, want ErrUnsatisfiable", err)
	}
}

// 语义 8：不修改入参，连续调用结果逐项相同。
func TestParseDeterministic(t *testing.T) {
	header := "bytes=100-199,0-99,-50"
	first, err := Parse(header, 1000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(header, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("not deterministic: %v vs %v", first, second)
	}
	if header != "bytes=100-199,0-99,-50" {
		t.Errorf("header mutated: %q", header)
	}
	want := []Range{{0, 199}, {950, 999}}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("result = %v, want %v", first, want)
	}
}
