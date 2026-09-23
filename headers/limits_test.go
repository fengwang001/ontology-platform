package headers_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/headers"
)

// 四类超限：彼此可判定，且拒绝不改变任何已解析状态。
func TestLimits(t *testing.T) {
	lim := headers.Limits{MaxEntries: 2, MaxNameLen: 4, MaxValueLen: 5, MaxBytes: 30}
	cfg := &headers.Config{Width: 78, Limits: lim}

	cases := []struct {
		name string
		run  func(s *headers.Set) error
		want error
	}{
		{"条数超限", func(s *headers.Set) error { return s.Add("C", "x") }, headers.ErrTooManyEntries},
		{"名字超长", func(s *headers.Set) error { return s.Add("Abcde", "x") }, headers.ErrNameTooLong},
		{"值超长", func(s *headers.Set) error { return s.Add("C", "123456") }, headers.ErrValueTooLong},
	}
	for _, c := range cases {
		s, err := headers.Parse([]byte("Abc: ok\r\nDef: ok\r\n\r\n"), cfg)
		if err != nil {
			t.Fatalf("基准解析失败: %v", err)
		}
		before := s.Bytes()
		if err := c.run(s); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if string(s.Bytes()) != string(before) {
			t.Errorf("%s: 拒绝后状态发生变化", c.name)
		}
		// 四类错误彼此可判定
		for _, other := range []error{headers.ErrTooManyEntries, headers.ErrNameTooLong,
			headers.ErrValueTooLong, headers.ErrTooLarge} {
			if other != c.want && errors.Is(c.run(s), other) {
				t.Errorf("%s: 错误与 %v 不可区分", c.name, other)
			}
		}
	}

	// 总字节超限（解析入口）
	big := "X-Long: " + strings.Repeat("v", 100) + "\r\n\r\n"
	if s, err := headers.Parse([]byte(big), cfg); !errors.Is(err, headers.ErrTooLarge) || s != nil {
		t.Errorf("总字节超限: err = %v, set = %v", err, s)
	}
	// 解析中的条数/名字/值超限同样拒绝且无半截集合
	if s, err := headers.Parse([]byte("A: 1\r\nB: 2\r\nC: 3\r\n\r\n"), cfg); !errors.Is(err, headers.ErrTooManyEntries) || s != nil {
		t.Errorf("解析条数超限: err = %v, set = %v", err, s)
	}
	if s, err := headers.Parse([]byte("Abcde: 1\r\n\r\n"), cfg); !errors.Is(err, headers.ErrNameTooLong) || s != nil {
		t.Errorf("解析名字超限: err = %v, set = %v", err, s)
	}
	if s, err := headers.Parse([]byte("Abc: 123456\r\n\r\n"), cfg); !errors.Is(err, headers.ErrValueTooLong) || s != nil {
		t.Errorf("解析值超限: err = %v, set = %v", err, s)
	}
}
