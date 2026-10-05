// Package rule 判定作业的 when 条件在给定上游结果下的去向。
//
// 本包是纯函数包：不感知图结构与作业状态机，只把
// (when, bad, skip) 映射为唯一去向，便于穷举测试与复现。
package rule

// When 是作业的运行条件。
type When int

const (
	OnSuccess When = iota
	OnFailure
	Always
	Manual
)

// Parse 把规格字符串解析为 When，第二个返回值报告是否合法。
func Parse(s string) (When, bool) {
	switch s {
	case "on_success":
		return OnSuccess, true
	case "on_failure":
		return OnFailure, true
	case "always":
		return Always, true
	case "manual":
		return Manual, true
	}
	return 0, false
}

func (w When) String() string {
	switch w {
	case OnSuccess:
		return "on_success"
	case OnFailure:
		return "on_failure"
	case Always:
		return "always"
	case Manual:
		return "manual"
	}
	return "unknown"
}

// Decision 是评估结果：作业应离开 Created 后进入的状态类别。
type Decision int

const (
	ToPending Decision = iota
	ToSkipped
	ToManual
)

func (d Decision) String() string {
	switch d {
	case ToPending:
		return "Pending"
	case ToSkipped:
		return "Skipped"
	case ToManual:
		return "Manual"
	}
	return "unknown"
}

// Evaluate 按 when 与上游汇总标记给出唯一去向。
//
// bad 为真表示存在某个 need 为 Failed 且其 allowFailure 为假；
// skip 为真表示存在某个 need 为 Skipped 或 Canceled。
// 非阻塞人工闸（Manual 且 allowFailure）对下游视同 Success，
// 因此既不贡献 bad 也不贡献 skip。
func Evaluate(w When, bad, skip bool) Decision {
	switch w {
	case OnSuccess:
		if bad || skip {
			return ToSkipped
		}
		return ToPending
	case OnFailure:
		// 上游被跳过不算失败；无 needs 时 bad 必为假，同为 Skipped。
		if bad {
			return ToPending
		}
		return ToSkipped
	case Always:
		return ToPending
	case Manual:
		if bad || skip {
			return ToSkipped
		}
		return ToManual
	}
	return ToSkipped
}
