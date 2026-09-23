// Package smj 实现两路已排序输入的等值排序合并连接，
// 右侧同键组在超阈值时溢出磁盘并在每个左行时回退重扫。
package smj

import "errors"

// ErrUnsorted：某侧输入未按键升序。错误中带侧别与第一处逆序位置。
var ErrUnsorted = errors.New("smj: input not sorted by key")
