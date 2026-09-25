// Package mask 提供列级脱敏的纯函数与按 (role, column) 的分派。
// 不依赖其他包。
package mask

import (
	"errors"
	"strings"
	"sync/atomic"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrUnknownRole   = errors.New("mask: unknown role")
	ErrUnknownColumn = errors.New("mask: unknown column")
)

type ruleFn func(raw string) string

// rules 按 (role, column) 直接索引，查找为 O(1)，不随规则条数线性扫描。
var rules = map[[2]string]ruleFn{}

// knownRoles 记录已注册的角色；未注册角色一律报 ErrUnknownRole。
var knownRoles = map[string]bool{}

// lastChecked 记录最近一次 MaskColumn 为解析 (role, column) 检查过的规则条数。
// 非导出，不出现在任何公开接口；仅包内测试可观测。
var lastChecked atomic.Int64

func init() {
	for _, col := range []string{"name", "card", "phone"} {
		RegisterRule("none", col, func(string) string { return "" })
		RegisterRule("admin", col, func(raw string) string { return strings.Clone(raw) })
	}
	RegisterRule("analyst", "name", MaskName)
	RegisterRule("analyst", "card", MaskCard)
	RegisterRule("analyst", "phone", MaskPhone)
}

// RegisterRule 注册一条 (role, column) 掩码规则。须在 MaskColumn 并发调用前完成注册。
func RegisterRule(role, column string, fn ruleFn) {
	knownRoles[role] = true
	rules[[2]string{role, column}] = fn
}

// MaskName 保留首字符，其余逐个替换为 *；长 0 返回 ""，长 1 返回原值。
func MaskName(raw string) string {
	r := []rune(raw)
	if len(r) <= 1 {
		return raw
	}
	var b strings.Builder
	b.WriteRune(r[0])
	b.WriteString(strings.Repeat("*", len(r)-1))
	return b.String()
}

// maskLast4 保留末尾 4 个字符，其余替换为 *；长度 < 4 时全部替换为 *。
func maskLast4(raw string) string {
	r := []rune(raw)
	if len(r) < 4 {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", len(r)-4) + string(r[len(r)-4:])
}

// MaskCard 卡号掩码：保留末 4，长度 < 4 全掩。
func MaskCard(raw string) string { return maskLast4(raw) }

// MaskPhone 手机号掩码：保留末 4，长度 < 4 全掩。
func MaskPhone(raw string) string { return maskLast4(raw) }

// MaskColumn 按角色与列分派掩码。纯函数：同一 (role, column, raw) 必得同一输出。
// 未知角色 / 未知列名整体失败，不触碰任何状态。
func MaskColumn(role, column, raw string) (string, error) {
	if !knownRoles[role] {
		return "", ErrUnknownRole
	}
	fn, ok := rules[[2]string{role, column}] // 直接索引：只检查 1 条规则
	lastChecked.Store(1)
	if !ok {
		return "", ErrUnknownColumn
	}
	return fn(raw), nil
}
