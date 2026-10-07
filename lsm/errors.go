package lsm

import "errors"

// 错误类别，按必须优先报告的次序排列（越小越靠前）。
// 当一次调用同时触及多类错误时，只报告次序最靠前的一类。
var (
	// ErrInvalidArgument 参数非法：配置非法、键区间 min>max、文件编号重复、
	// 输出文件落在错误层、输出文件编号互相重复等。
	ErrInvalidArgument = errors.New("lsm: invalid argument")
	// ErrFileNotFound 文件不存在。
	ErrFileNotFound = errors.New("lsm: file not found")
	// ErrFilePinned 文件已被某个未结束的计划占用。
	ErrFilePinned = errors.New("lsm: file pinned by in-flight plan")
	// ErrPlanNotFound 计划不存在（已安装、已取消或从未创建）。
	ErrPlanNotFound = errors.New("lsm: plan not found")
	// ErrInvariant 层不变量被破坏：非零层文件区间出现非端点相接的重叠。
	ErrInvariant = errors.New("lsm: level invariant violated")
)

// errorRank 给出各类错误的报告优先级，数值越小越靠前。
func errorRank(err error) int {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return 0
	case errors.Is(err, ErrFileNotFound):
		return 1
	case errors.Is(err, ErrFilePinned):
		return 2
	case errors.Is(err, ErrPlanNotFound):
		return 3
	case errors.Is(err, ErrInvariant):
		return 4
	default:
		return 5
	}
}

// firstError 在候选错误中返回报告次序最靠前的一个；全为 nil 时返回 nil。
func firstError(errs ...error) error {
	var best error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if best == nil || errorRank(err) < errorRank(best) {
			best = err
		}
	}
	return best
}
