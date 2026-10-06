// Package limitbook 实现家庭医疗保单的多层限额账引擎。
//
// 引擎由保单与成员登记、保单年度划分、限额批改、理赔扣减与冲正
// 四个协作模块构成，在项目年度限额、个人年度限额、家庭共享年度
// 限额与个人终身限额四层约束下给出唯一且可精确复现的赔付额。
package limitbook

import (
	"errors"
	"fmt"
)

// Kind 区分各类业务错误，拒绝次序按常量声明顺序由先到后。
type Kind int

const (
	ErrInvalidParam    Kind = iota // 参数非法
	ErrPolicyNotFound              // 保单不存在
	ErrMemberNotFound              // 成员不存在
	ErrItemNotFound                // 项目不存在
	ErrMemberDuplicate             // 成员重复
	ErrItemDuplicate               // 项目重复
	ErrClaimExists                 // 理赔已存在
	ErrClaimNotFound               // 理赔不存在
	ErrNotLast                     // 非末笔
	ErrCapped                      // 已封顶
	ErrRetroactive                 // 追溯批改
	ErrDayNotCovered               // 发生日未承保
)

var kindText = map[Kind]string{
	ErrInvalidParam:    "参数非法",
	ErrPolicyNotFound:  "保单不存在",
	ErrMemberNotFound:  "成员不存在",
	ErrItemNotFound:    "项目不存在",
	ErrMemberDuplicate: "成员重复",
	ErrItemDuplicate:   "项目重复",
	ErrClaimExists:     "理赔已存在",
	ErrClaimNotFound:   "理赔不存在",
	ErrNotLast:         "非末笔",
	ErrCapped:          "已封顶",
	ErrRetroactive:     "追溯批改",
	ErrDayNotCovered:   "发生日未承保",
}

func (k Kind) String() string {
	if s, ok := kindText[k]; ok {
		return s
	}
	return "未知错误"
}

// Error 是引擎返回的业务错误，Kind 字段可用于精确区分错误类别。
type Error struct {
	Kind   Kind
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Kind.String()
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

func newErr(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Detail: fmt.Sprintf(format, args...)}
}

// IsKind 报告 err 是否为类别 k 的业务错误。
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}
