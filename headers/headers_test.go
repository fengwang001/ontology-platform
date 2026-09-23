package headers_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"ontology/headers"
)

func mustParse(t *testing.T, in string) *headers.Set {
	t.Helper()
	s, err := headers.Parse([]byte(in), nil)
	if err != nil {
		t.Fatalf("Parse(%q) err %v", in, err)
	}
	return s
}

func TestOrderAndDuplicates(t *testing.T) {
	s := mustParse(t, "B: 1\r\nA: 2\r\nB: 3\r\nC: 4\r\nB: 5\r\n\r\n")
	if got := s.GetAll("B"); !slices.Equal(got, []string{"1", "3", "5"}) {
		t.Errorf("GetAll(B) = %v, 保序不去重失败", got)
	}
	if s.Len() != 5 || s.Count("B") != 3 || s.Count("A") != 1 {
		t.Errorf("Len/Count 错误: %d %d %d", s.Len(), s.Count("B"), s.Count("A"))
	}
	want := "B: 1\r\nA: 2\r\nB: 3\r\nC: 4\r\nB: 5\r\n\r\n"
	if string(s.Bytes()) != want {
		t.Errorf("回写顺序改变: %q", s.Bytes())
	}
}

func TestMergePolicies(t *testing.T) {
	s := mustParse(t, "X-A: p\r\nX-A: q\r\nX-Last: 1\r\nX-Last: 2\r\n"+
		"X-Multi: a\r\nX-Multi: b\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n")
	cases := []struct {
		name    string
		want    string
		wantErr error
	}{
		{"X-A", "p", nil},
		{"X-Last", "2", nil},
		{"X-Multi", "a, b", nil},
		{"Content-Length", "", headers.ErrDuplicate},
	}
	for _, c := range cases {
		got, err := s.Get(c.name)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("Get(%q) = %q, %v; want %q, %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}

func TestNormalization(t *testing.T) {
	s := mustParse(t, "content-TYPE:  text/html \r\nx-a:\t1\t\r\n\r\n")
	if !s.Normalized() {
		t.Error("应报告发生过规范化改写")
	}
	want := "Content-Type: text/html\r\nX-A: 1\r\n\r\n"
	if string(s.Bytes()) != want {
		t.Errorf("规范形态 = %q, want %q", s.Bytes(), want)
	}
	// 内部空白保留
	s2 := mustParse(t, "X-V: a  b\tc\r\n\r\n")
	if got := s2.GetAll("X-V")[0]; got != "a  b\tc" {
		t.Errorf("内部空白未保留: %q", got)
	}
	// 已规范输入：逐字节相同且不标记改写
	s3 := mustParse(t, want)
	if s3.Normalized() {
		t.Error("已规范输入不应标记改写")
	}
}

func TestByteExactRoundTrip(t *testing.T) {
	in := "Content-Type: text/html\r\nX-A: 1\r\nX-A: 2\r\nX-Empty:\r\n\r\n"
	s := mustParse(t, in)
	if !bytes.Equal(s.Bytes(), []byte(in)) {
		t.Errorf("规范输入回写不逐字节相同: %q", s.Bytes())
	}
}

func TestIdempotentWrite(t *testing.T) {
	in := "x-a:  1 \r\nX-B: 2\r\n folded  line\r\nX-C: 3\r\n\r\n"
	s1 := mustParse(t, in)
	b1 := s1.Bytes()
	s2, err := headers.Parse(b1, nil)
	if err != nil {
		t.Fatalf("reparse err %v", err)
	}
	if !bytes.Equal(b1, s2.Bytes()) {
		t.Errorf("幂等失败: %q != %q", b1, s2.Bytes())
	}
	if s2.Normalized() {
		t.Error("规范形态再解析不应标记改写")
	}
}

func TestNotFoundVsEmpty(t *testing.T) {
	s := mustParse(t, "X-Empty:\r\n\r\n")
	if _, err := s.Get("X-Missing"); !errors.Is(err, headers.ErrNotFound) {
		t.Errorf("不存在应返回 ErrNotFound, got %v", err)
	}
	v, err := s.Get("X-Empty")
	if err != nil || v != "" {
		t.Errorf("存在但为空应返回 (\"\", nil), got (%q, %v)", v, err)
	}
	if s.Has("X-Missing") || !s.Has("X-Empty") {
		t.Error("Has 区分失败")
	}
	if s.Count("X-Missing") != 0 || s.Count("X-Empty") != 1 {
		t.Error("Count 区分失败")
	}
}

func TestQueriesAreStable(t *testing.T) {
	s := mustParse(t, "B: 1\r\nA: 2\r\nB: 3\r\n\r\n")
	type snapshot struct {
		length int
		count  int
		total  int
		normal bool
	}
	snap := func() snapshot {
		return snapshot{s.Len(), s.Count("B"), s.TotalBytes(), s.Normalized()}
	}
	first, second := snap(), snap()
	if first != second {
		t.Errorf("连查两次结果不同: %+v != %+v", first, second)
	}
	if first.length != 3 || first.count != 2 || first.total != len("B: 1\r\nA: 2\r\nB: 3\r\n\r\n") {
		t.Errorf("查询结果错误: %+v", first)
	}
}

func TestSetAddDel(t *testing.T) {
	s := mustParse(t, "A: 1\r\nA: 2\r\nB: 3\r\n\r\n")
	if err := s.Set("a", "only"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAll("A"); !slices.Equal(got, []string{"only"}) {
		t.Errorf("Set 后 GetAll = %v", got)
	}
	if err := s.Add("A", "again"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAll("A"); !slices.Equal(got, []string{"only", "again"}) {
		t.Errorf("Add 后 GetAll = %v", got)
	}
	if n := s.Del("A"); n != 2 || s.Has("A") {
		t.Errorf("Del 删除 %d 条, Has=%v", n, s.Has("A"))
	}
	// 顺序保持：B 仍在，且回写只有 B
	if string(s.Bytes()) != "B: 3\r\n\r\n" {
		t.Errorf("删除后回写 = %q", s.Bytes())
	}
}
