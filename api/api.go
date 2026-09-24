// Package api 对外门面：schema 演进与旧事件到 active 列序的投影。依赖 mapc。
package api

import (
	"errors"
	"fmt"

	"ontology/mapc"
	"ontology/sch"
)

// Column 是对外暴露的列描述。
type Column = sch.Column

type Service struct {
	reg *sch.Registry
	mp  *mapc.Mapper
}

// New 可给定初始列作为 v1（对应题目 v1=[x:int,y:str,m:str]）；也可无参调用。
func New(init ...Column) *Service {
	return &Service{reg: sch.NewRegistry(init...), mp: mapc.New()}
}

func (s *Service) AddColumn(name, typ string, required bool) error {
	return s.reg.AddColumn(name, typ, required)
}

func (s *Service) DropColumn(name string) error { return s.reg.DropColumn(name) }

func (s *Service) ChangeType(name, newType string) error {
	return s.reg.ChangeType(name, newType)
}

// Map 把 version 版本的事件值投影到 active 列序。
func (s *Service) Map(version int, values []any) ([]any, error) {
	lay, err := s.reg.Layout(version)
	if err != nil {
		return nil, err
	}
	return s.mp.Map(lay, s.reg.Active(), values)
}

// Active 返回 active（最新版本）列布局。
func (s *Service) Active() []Column { return s.reg.Active() }

// SelfCheck 在独立实例上核验四条不变量，通过返回 nil。可并发调用。
func (s *Service) SelfCheck() error {
	for name, fn := range map[string]func() error{
		"不变量1/2 朴素一致且按名对齐": checkEvents,
		"不变量3 可判定":         checkDeterministic,
		"不变量4 失败不留痕":       checkNoSideEffect,
	} {
		if err := fn(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// sixVersion 复现题目六版演进。
func sixVersion() *Service {
	s := New(
		Column{Name: "x", Typ: sch.Int},
		Column{Name: "y", Typ: sch.Str},
		Column{Name: "m", Typ: sch.Str},
	) // v1
	s.AddColumn("z", "str", true)  // v2
	s.ChangeType("y", "int")       // v3
	s.ChangeType("x", "str")       // v4
	s.DropColumn("m")              // v5
	s.AddColumn("w", "int", false) // v6
	return s
}

// checkEvents 不变量1/2：六事件结果与朴素参照逐列一致、按名对齐（z="b"、w=0 不错位）。
func checkEvents() error {
	s := sixVersion()
	cases := []struct {
		ver  int
		vals []any
		want []any
		err  error
	}{
		{6, []any{"1", int64(2), "a", int64(3)}, []any{"1", int64(2), "a", int64(3)}, nil},
		{2, []any{int64(5), "6", "M", "b"}, []any{"5", int64(6), "b", int64(0)}, nil},
		{2, []any{int64(5), "abc", "M", "b"}, nil, sch.ErrBadValue},
		{4, []any{"9", int64(10), "M", "d"}, []any{"9", int64(10), "d", int64(0)}, nil},
		{1, []any{int64(11), "12", "M"}, nil, sch.ErrMissingColumn},
		{3, []any{int64(13), int64(14), "M", "e"}, []any{"13", int64(14), "e", int64(0)}, nil},
	}
	for i, c := range cases {
		got, err := s.Map(c.ver, c.vals)
		if !errors.Is(err, c.err) || (err == nil && fmt.Sprint(got) != fmt.Sprint(c.want)) {
			return fmt.Errorf("事件%d: %v,%v", i+1, got, err)
		}
	}
	return nil
}

// checkDeterministic 不变量3：同版本同值反复 Map 结果完全确定。
func checkDeterministic() error {
	s := sixVersion()
	first, err1 := s.Map(2, []any{int64(5), "6", "M", "b"})
	for i := 0; i < 8; i++ {
		got, err := s.Map(2, []any{int64(5), "6", "M", "b"})
		if (err == nil) != (err1 == nil) || fmt.Sprint(got) != fmt.Sprint(first) {
			return fmt.Errorf("第%d次结果漂移", i)
		}
	}
	return nil
}

// checkNoSideEffect 不变量4：被拒操作零写入，拒绝后仍可正常使用。
func checkNoSideEffect() error {
	s := sixVersion()
	before := fmt.Sprint(s.Active())
	bads := []error{
		s.AddColumn("q", "float", false), s.AddColumn("", "int", false),
		s.AddColumn("x", "int", false), s.ChangeType("no", "int"), s.DropColumn("no"),
	}
	for _, e := range bads {
		if e == nil {
			return errors.New("非法操作未被拒")
		}
	}
	if _, err := s.Map(99, nil); !errors.Is(err, sch.ErrVersionUnregistered) {
		return fmt.Errorf("未注册版本: %v", err)
	}
	if _, err := s.Map(1, []any{int64(1), "2", "M"}); !errors.Is(err, sch.ErrMissingColumn) {
		return fmt.Errorf("缺 required 列: %v", err)
	}
	if _, err := s.Map(2, []any{int64(5), "abc", "M", "b"}); !errors.Is(err, sch.ErrBadValue) {
		return fmt.Errorf("坏值: %v", err)
	}
	if fmt.Sprint(s.Active()) != before {
		return errors.New("被拒操作改变了状态")
	}
	_, err := s.Map(6, []any{"1", int64(2), "a", int64(3)})
	return err
}
