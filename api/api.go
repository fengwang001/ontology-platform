// Package api 是对外门面：增量 join 右表哈希索引的公开接口。依赖 rjoin。
package api

import (
	"errors"
	"fmt"

	"ontology/rjoin"
)

// 对外暴露的四类可判定哨兵错误，互不相同。
var (
	ErrEmptyLeftID   = rjoin.ErrEmptyLeftID
	ErrEmptyLeftKey  = rjoin.ErrEmptyLeftKey
	ErrDupLeftID     = rjoin.ErrDupLeftID
	ErrEmptyRightKey = rjoin.ErrEmptyRightKey
)

// Service 是一个增量 join 实例，可并发使用。
type Service struct {
	j *rjoin.Join
}

// New 返回空实例。
func New() *Service {
	return &Service{j: rjoin.New()}
}

// Left 登记左事件。
func (s *Service) Left(leftID, key string) error { return s.j.Left(leftID, key) }

// UpsertRight 写右表并重估受影响左事件。
func (s *Service) UpsertRight(key, val string) error { return s.j.UpsertRight(key, val) }

// DeleteRight 删右表并把受影响左事件置 ∅。
func (s *Service) DeleteRight(key string) error { return s.j.DeleteRight(key) }

// GetView 返回 leftID 当前视图值及是否匹配。
func (s *Service) GetView(leftID string) (string, bool) { return s.j.GetView(leftID) }

// SelfCheck 在独立的全新实例上跑内置操作序列，核验四条不变量。
// 不触碰 s.j，因此可与 GetView 并发调用。
func (s *Service) SelfCheck() error {
	j := rjoin.New()
	// 不变量 1、3：九步序列后视图与批量重算一致，更新/删除立即生效。
	seq := []func() error{
		func() error { return j.UpsertRight("a", "v1") },
		func() error { return j.Left("L1", "a") },
		func() error { return j.UpsertRight("b", "w1") },
		func() error { return j.Left("L2", "b") },
		func() error { return j.UpsertRight("a", "v2") },
		func() error { return j.Left("L3", "a") },
		func() error { return j.DeleteRight("b") },
		func() error { return j.Left("L4", "b") },
		func() error { return j.UpsertRight("b", "w2") },
	}
	for i, op := range seq {
		if err := op(); err != nil {
			return fmt.Errorf("自检第 %d 步失败: %w", i+1, err)
		}
	}
	want := []struct {
		id  string
		val string
		ok  bool
	}{{"L1", "v2", true}, {"L2", "", false}, {"L3", "v2", true}, {"L4", "w2", true}}
	for _, w := range want {
		if v, ok := j.GetView(w.id); v != w.val || ok != w.ok {
			return fmt.Errorf("不变量1/3 破坏: GetView(%q)=(%q,%v)，期望 (%q,%v)", w.id, v, ok, w.val, w.ok)
		}
	}
	// 不变量 2：反向索引内部一致。
	if err := j.Check(); err != nil {
		return fmt.Errorf("不变量2 破坏: %w", err)
	}
	// 不变量 4：四类拒绝各自返回对应哨兵错误，且状态不变。
	before1, _ := j.GetView("L1")
	before4, _ := j.GetView("L4")
	rejects := []struct {
		op   func() error
		want error
	}{
		{func() error { return j.Left("", "a") }, ErrEmptyLeftID},
		{func() error { return j.Left("L9", "") }, ErrEmptyLeftKey},
		{func() error { return j.Left("L1", "a") }, ErrDupLeftID},
		{func() error { return j.UpsertRight("", "x") }, ErrEmptyRightKey},
		{func() error { return j.DeleteRight("") }, ErrEmptyRightKey},
	}
	for i, r := range rejects {
		if err := r.op(); !errors.Is(err, r.want) {
			return fmt.Errorf("拒绝 %d 错误不符: got %v want %v", i, err, r.want)
		}
	}
	if v, _ := j.GetView("L1"); v != before1 {
		return errors.New("不变量4 破坏: 被拒操作改变了 L1 视图")
	}
	if v, _ := j.GetView("L4"); v != before4 {
		return errors.New("不变量4 破坏: 被拒操作改变了 L4 视图")
	}
	return j.Check()
}
