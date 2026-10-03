// Package view 提供按角色权限控制的审计事件交叉统计入口。
package view

import (
	"errors"

	"ontology/agg"
	"ontology/disclose"
)

var (
	// ErrUnknownRole 表示角色未在角色表中声明。
	ErrUnknownRole = errors.New("view: 角色未知")
	// ErrNoPermission 表示维度敏感级别超过角色可见级别。
	ErrNoPermission = errors.New("view: 无权限")
)

// Tabulate 统计 [from,to) 内事件在 rowDim×colDim 上的交叉表并施加小格抑制。
// 拒绝顺序：参数非法 → 角色未知 → 无权限 → 规模超限，只报第一个；
// 被拒绝时不改任何状态。k 取两维敏感级别较大者对应的 kByLevel 项。
func Tabulate(s *agg.Store, role, rowDim, colDim string, from, to int64) (disclose.Table, error) {
	if from < 0 || from >= to || to > agg.MaxTs || rowDim == colDim {
		return disclose.Table{}, agg.ErrInvalidParam
	}
	rowLv, ok := s.DimLevel(rowDim)
	if !ok {
		return disclose.Table{}, agg.ErrInvalidParam
	}
	colLv, ok := s.DimLevel(colDim)
	if !ok {
		return disclose.Table{}, agg.ErrInvalidParam
	}
	roleLv, ok := s.RoleLevel(role)
	if !ok {
		return disclose.Table{}, ErrUnknownRole
	}
	if rowLv > roleLv || colLv > roleLv {
		return disclose.Table{}, ErrNoPermission
	}
	res, err := s.Query(rowDim, colDim, from, to)
	if err != nil {
		return disclose.Table{}, err
	}
	k := s.KFor(max(rowLv, colLv))
	return disclose.Suppress(res.Rows, res.Cols, res.Counts, k), nil
}
