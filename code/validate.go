package code

import "errors"

// 代码结构相关的哨兵错误：
//
//   - ErrArgument：参数非法（Step 的 name、Branch 的 pid 为空，项种类未知，
//     以及项数越界——这些在拒绝次序上都属于“参数非法”）；
//   - ErrCode：结构非法，即 Branch 的 N/O 中出现嵌套 Branch。
//
// 参数非法优先于 ErrCode 被报告（Validate 先逐字段检查，再检查嵌套）。
var (
	ErrArgument = errors.New("code: invalid argument")
	ErrCode     = errors.New("code: invalid code structure")
)

// Validate 检查代码是否可用于重放：
//
//  1. 顶层项数在 1..MaxItems；
//  2. 每个 Step 的 name 非空，每个 Branch 的 pid 非空、项种类已知；
//  3. 每个 Branch 的 N、O 长度各不超过 MaxBranchItems，且只含 Step；
//     出现嵌套 Branch 或未知项即 ErrCode。
//
// 按先参数、后结构的次序返回遇到的第一个错误。
func (c Code) Validate() error {
	if len(c) < 1 || len(c) > MaxItems {
		return ErrArgument
	}
	for _, it := range c {
		switch it.Kind {
		case KindStep:
			if len(it.Name) == 0 {
				return ErrArgument
			}
		case KindBranch:
			if len(it.Pid) == 0 {
				return ErrArgument
			}
		default:
			return ErrArgument
		}
	}
	for _, it := range c {
		if it.Kind != KindBranch {
			continue
		}
		if len(it.New) > MaxBranchItems || len(it.Old) > MaxBranchItems {
			return ErrArgument
		}
		for _, sub := range it.New {
			if err := validateBranchStep(sub); err != nil {
				return err
			}
		}
		for _, sub := range it.Old {
			if err := validateBranchStep(sub); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateBranchStep 检查 N/O 中的单个项：只允许非空 name 的 Step。
// 空 name 属参数非法；Branch 嵌套或未知种类属 ErrCode。
func validateBranchStep(sub Item) error {
	switch sub.Kind {
	case KindStep:
		if len(sub.Name) == 0 {
			return ErrArgument
		}
		return nil
	case KindBranch:
		return ErrCode
	default:
		return ErrCode
	}
}
