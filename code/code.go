// Package code 描述工作流代码：一个项序列，项为 Step(name)
// 或 Branch(pid, N, O)。N 与 O 是只含 Step 的序列（可为空），
// Branch 不得嵌套。Validate 负责全部静态校验。
package code

import (
	"bytes"
	"errors"
	"fmt"
)

// 静态约束。
const (
	// MaxItems 是代码总项数上限（下限为 1）。
	MaxItems = 1000
	// MaxBranchItems 是 Branch 的 N、O 各自的项数上限。
	MaxBranchItems = 1000
)

// ErrCode 表示代码本身非法（项数越界、空 name/pid、Branch 嵌套等）。
var ErrCode = errors.New("code: invalid workflow code")

// Item 是代码中的一项，实现者为 Step 与 Branch。
type Item interface{ isItem() }

// Step 是步骤项 S(name)，name 为非空字节串。
type Step struct {
	Name []byte
}

// Branch 是补丁分支项 Branch(pid, N, O)：pid 命中（历史有标记
// 或本次运行已打补丁）走 N，否则走 O。N、O 只含 Step。
type Branch struct {
	Pid []byte
	New []Item
	Old []Item
}

func (Step) isItem()   {}
func (Branch) isItem() {}

// Code 是工作流代码，即项序列。
type Code []Item

// Validate 校验代码合法性，非法时返回包裹 ErrCode 的错误。
func Validate(c Code) error {
	if len(c) == 0 || len(c) > MaxItems {
		return fmt.Errorf("%w: %d items, want 1..%d", ErrCode, len(c), MaxItems)
	}
	for _, it := range c {
		if err := validateItem(it); err != nil {
			return err
		}
	}
	return nil
}

func validateItem(it Item) error {
	switch v := it.(type) {
	case Step:
		return validateStep(v)
	case Branch:
		if len(v.Pid) == 0 {
			return fmt.Errorf("%w: empty patch id", ErrCode)
		}
		for _, seq := range [2][]Item{v.New, v.Old} {
			if len(seq) > MaxBranchItems {
				return fmt.Errorf("%w: branch body has %d items, want <=%d",
					ErrCode, len(seq), MaxBranchItems)
			}
			for _, sub := range seq {
				s, ok := sub.(Step)
				if !ok {
					return fmt.Errorf("%w: nested branch is not allowed", ErrCode)
				}
				if err := validateStep(s); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown item type %T", ErrCode, it)
	}
}

func validateStep(s Step) error {
	if len(s.Name) == 0 {
		return fmt.Errorf("%w: empty step name", ErrCode)
	}
	return nil
}

// Equal 报告两份代码是否逐项相同（用于测试与调试输出）。
func Equal(a, b Code) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		switch x := a[i].(type) {
		case Step:
			y, ok := b[i].(Step)
			if !ok || !bytes.Equal(x.Name, y.Name) {
				return false
			}
		case Branch:
			y, ok := b[i].(Branch)
			if !ok || !bytes.Equal(x.Pid, y.Pid) ||
				!Equal(x.New, y.New) || !Equal(x.Old, y.Old) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
