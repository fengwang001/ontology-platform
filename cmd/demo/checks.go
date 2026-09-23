package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"ontology/headers"
	"ontology/listval"
	"ontology/policy"
)

type check struct {
	name string
	fn   func() error
}

func registry() *policy.Registry {
	reg := policy.NewRegistry(policy.Policy{Dup: policy.DupFirst})
	reg.Register(policy.Policy{List: true, Dup: policy.DupMerge}, "Accept", "X-Multi")
	reg.Register(policy.Policy{Dup: policy.DupLast}, "X-Last")
	reg.Register(policy.Policy{Dup: policy.DupError}, "X-Once")
	return reg
}

var checks = []check{
	{"保序与重复不去重", checkOrder},
	{"四种单值策略", checkDupPolicies},
	{"折行往返无损", checkFoldRoundTrip},
	{"首行续行判错", checkLeadingContinuation},
	{"冒号前空白判错", checkNameColonSpace},
	{"注入变体全拒", checkInjection},
	{"引号内逗号不切", checkQuotedComma},
	{"列表往返等价", checkListRoundTrip},
	{"回写幂等", checkIdempotent},
	{"截断点遍历可判定", checkTruncation},
	{"四类超限状态零变化", checkLimits},
	{"查找比较数不随N增长", checkFindComplexity},
	{"并发查询与串行一致", checkConcurrency},
}

func checkOrder() error {
	s, err := headers.Parse([]byte("B: 1\r\nA: x\r\nA: y\r\nB: 2\r\n\r\n"), registry(), headers.Config{})
	if err != nil {
		return err
	}
	if got := s.GetAll("a"); strings.Join(got, ",") != "x,y" {
		return fmt.Errorf("A 保序失败: %v", got)
	}
	if got := s.GetAll("B"); strings.Join(got, ",") != "1,2" {
		return fmt.Errorf("B 保序失败: %v", got)
	}
	if s.Len() != 4 || s.Count("A") != 2 {
		return fmt.Errorf("计数错误: len=%d count=%d", s.Len(), s.Count("A"))
	}
	return nil
}

func checkDupPolicies() error {
	s, err := headers.Parse([]byte("X-Multi: a, b\r\nX-Multi: c\r\nX-Last: 1\r\nX-Last: 2\r\n"+
		"X-First: 1\r\nX-First: 2\r\nX-Once: 1\r\nX-Once: 2\r\n\r\n"), registry(), headers.Config{})
	if err != nil {
		return err
	}
	cases := []struct{ name, want string }{
		{"X-Multi", "a, b, c"}, {"X-Last", "2"}, {"X-First", "1"},
	}
	for _, c := range cases {
		got, err := s.Get(c.name)
		if err != nil || got != c.want {
			return fmt.Errorf("%s = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := s.Get("X-Once"); !errors.Is(err, policy.ErrDuplicate) {
		return fmt.Errorf("DupError 未报错: %v", err)
	}
	return nil
}

func checkFoldRoundTrip() error {
	long := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	s := headers.New(registry(), headers.Config{FoldWidth: 40})
	if err := s.Set("X-Long", long); err != nil {
		return err
	}
	back, err := headers.Parse(s.Marshal(), registry(), headers.Config{})
	if err != nil {
		return err
	}
	if got, _ := back.Get("X-Long"); got != long {
		return fmt.Errorf("往返后值改变: %q", got)
	}
	return nil
}

func checkLeadingContinuation() error {
	_, err := headers.Parse([]byte(" x\r\nA: 1\r\n\r\n"), nil, headers.Config{})
	var pe *headers.ParseError
	if !errors.As(err, &pe) || pe.Stage != headers.StageFold {
		return fmt.Errorf("未判为折行阶段错误: %v", err)
	}
	return nil
}

func checkNameColonSpace() error {
	_, err := headers.Parse([]byte("X : 1\r\n\r\n"), nil, headers.Config{})
	if !errors.Is(err, headers.ErrNameColonSpace) {
		return fmt.Errorf("未判为冒号前空白: %v", err)
	}
	return nil
}

func checkInjection() error {
	variants := []string{
		"a\r\nEvil: 1", "a\nEvil: 1", "a\rEvil: 1", "a\x00b",
		"a%0d%0aEvil:%201", `a\r\nEvil: 1`, "a⃍Evil",
	}
	s := headers.New(registry(), headers.Config{})
	for _, v := range variants {
		if err := s.Set("X", v); err == nil {
			return fmt.Errorf("变体 %q 未被拒绝", v)
		}
	}
	if bytes.Contains(s.Marshal(), []byte("Evil")) {
		return errors.New("回写字节含注入头部")
	}
	return nil
}

func checkQuotedComma() error {
	s, err := headers.Parse([]byte("Accept: text/html, text/plain;level=\"1,2\"\r\n\r\n"), registry(), headers.Config{})
	if err != nil {
		return err
	}
	items, err := s.List("Accept")
	if err != nil || len(items) != 2 || items[1] != `text/plain;level="1,2"` {
		return fmt.Errorf("切分错误: %v, %v", items, err)
	}
	return nil
}

func checkListRoundTrip() error {
	src := `a, b="x,\"y\", z", c`
	items, err := listval.Split(src)
	if err != nil {
		return err
	}
	joined, err := listval.Join(items)
	if err != nil {
		return err
	}
	again, err := listval.Split(joined)
	if err != nil {
		return err
	}
	if strings.Join(items, "|") != strings.Join(again, "|") {
		return fmt.Errorf("往返不等价: %v vs %v", items, again)
	}
	return nil
}

func checkIdempotent() error {
	canonical := "A-B: x\r\nC: y\r\n\r\n"
	s, err := headers.Parse([]byte(canonical), nil, headers.Config{})
	if err != nil {
		return err
	}
	if string(s.Marshal()) != canonical {
		return errors.New("规范输入回写不逐字节相等")
	}
	messy, err := headers.Parse([]byte("a-b:  x \r\nC: y\n\r\n"), nil, headers.Config{})
	if err != nil {
		return err
	}
	if !messy.Normalized() {
		return errors.New("未标记规范化改写")
	}
	m1 := messy.Marshal()
	again, err := headers.Parse(m1, nil, headers.Config{})
	if err != nil {
		return err
	}
	if !bytes.Equal(m1, again.Marshal()) || again.Normalized() {
		return errors.New("规范形态再解析再回写不幂等")
	}
	return nil
}
