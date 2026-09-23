package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/headers"
)

func checkTruncation() error {
	full := "A: 1\r\nLong-Name: some value here\r\n\tcontinued\r\n\r\n"
	if _, err := headers.Parse([]byte(full), nil, headers.Config{}); err != nil {
		return fmt.Errorf("完整输入应成功: %v", err)
	}
	for i := 0; i < len(full); i++ {
		s, err := headers.Parse([]byte(full[:i]), nil, headers.Config{})
		var pe *headers.ParseError
		if !errors.As(err, &pe) {
			return fmt.Errorf("截断点 %d 未返回可判定错误: %v", i, err)
		}
		if s != nil {
			return fmt.Errorf("截断点 %d 返回了半截集合", i)
		}
	}
	return nil
}

func checkLimits() error {
	good := "A: 1\r\n\r\n"
	cases := []struct {
		cfg  headers.Config
		in   string
		want error
	}{
		{headers.Config{MaxHeaders: 1}, "A: 1\r\nB: 2\r\n\r\n", headers.ErrTooManyHeaders},
		{headers.Config{MaxName: 2}, "Abc: 1\r\n\r\n", headers.ErrNameTooLong},
		{headers.Config{MaxValue: 2}, "A: 123\r\n\r\n", headers.ErrValueTooLong},
		{headers.Config{MaxBytes: 4}, good, headers.ErrInputTooLarge},
	}
	for i, c := range cases {
		s := headers.New(nil, c.cfg)
		if err := s.Parse([]byte(c.in)); !errors.Is(err, c.want) {
			return fmt.Errorf("超限 %d 得到 %v，期望 %v", i, err, c.want)
		}
		if s.Len() != 0 || s.ByteLen() != 2 {
			return fmt.Errorf("超限 %d 后状态被改变", i)
		}
	}
	return nil
}

func checkFindComplexity() error {
	compares := func(n int) (int, error) {
		s := headers.New(nil, headers.Config{MaxHeaders: n + 1})
		for i := 0; i < n; i++ {
			if err := s.Add(fmt.Sprintf("H-%d", i), "v"); err != nil {
				return 0, err
			}
		}
		if _, err := s.Get("H-7"); err != nil {
			return 0, err
		}
		return s.LastFindCompares(), nil
	}
	c50, err := compares(50)
	if err != nil {
		return err
	}
	c5000, err := compares(5000)
	if err != nil {
		return err
	}
	if c50 != c5000 {
		return fmt.Errorf("比较数随 N 增长: N=50 为 %d, N=5000 为 %d", c50, c5000)
	}
	s := headers.New(nil, headers.Config{})
	_ = s.Add("A", "1")
	_ = s.Add("B", "2")
	_ = s.Add("A", "3")
	s.Del("B") // 触发索引重建
	if got := s.GetAll("A"); strings.Join(got, ",") != "1,3" {
		return fmt.Errorf("索引重建后保序被破坏: %v", got)
	}
	return nil
}

func checkConcurrency() error {
	s, err := headers.Parse([]byte("A: 1\r\nA: 2\r\nB: x\r\n\r\n"), nil, headers.Config{})
	if err != nil {
		return err
	}
	serial := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		v, _ := s.Get("A")
		serial = append(serial, fmt.Sprintf("%v|%d|%d|%v", v, s.Len(), s.Count("A"), s.Has("B")))
	}
	parallel := make([]string, 400)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g * 50; i < g*50+50; i++ {
				v, _ := s.Get("A")
				parallel[i] = fmt.Sprintf("%v|%d|%d|%v", v, s.Len(), s.Count("A"), s.Has("B"))
			}
		}(g)
	}
	wg.Wait()
	for i := range serial {
		if serial[i] != parallel[i] {
			return fmt.Errorf("并发结果与串行不一致 at %d", i)
		}
	}
	return nil
}
