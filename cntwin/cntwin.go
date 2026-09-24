// Package cntwin 提供计数翻滚窗口的纯判定函数：窗口归属、触发、迟到与丢弃。
// 不依赖任何其他包；所有函数假定参数已校验（pos>=0, size>0, lateness>=0）。
package cntwin

// Window 返回位置 pos 所属的窗口号：k = floor(pos/size)，窗口 [k*size,(k+1)*size) 左闭右开。
func Window(pos, size int64) int64 { return pos / size }

// Triggered 报告窗口已接受元素数 cnt 是否达到 size（触发并关闭的条件）。
func Triggered(cnt, size int64) bool { return cnt == size }

// Late 报告位置 pos 相对到达前水位 wm 是否迟到（已有更大的 Pos 到达）。
func Late(pos, wm int64) bool { return pos < wm }

// Acceptable 报告迟到元素是否仍在允许范围内：pos >= wm-lateness（含边界）。
func Acceptable(pos, wm, lateness int64) bool { return pos >= wm-lateness }
