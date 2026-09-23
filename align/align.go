// Package align 把纳秒时间戳对齐到以绝对时间原点为锚的固定步长时间桶。
package align

import "errors"

var (
	// ErrInvalidStep 表示步长为 0 或负数。步长不整除跨度是合法的。
	ErrInvalidStep = errors.New("align: step must be positive")
	// ErrLate 表示点迟到超过窗口容量所允许的桶跨度，无法再并入已吐出的桶。
	ErrLate = errors.New("align: point later than window allows")
)

// BucketStart 返回时间戳所属桶的起点：floor(ts/step)*step，向下取整。
// step 必须为正数，否则返回 ErrInvalidStep。
func BucketStart(ts, step int64) (int64, error) {
	if step <= 0 {
		return 0, ErrInvalidStep
	}
	q, r := ts/step, ts%step
	if r != 0 && ts < 0 {
		q-- // Go 整数除法向零取整；负余数时必须向负无穷退一档。
	}
	return q * step, nil
}

// BucketIndex 是桶起点在 step 网格上的序号 floor(ts/step)。
func BucketIndex(ts, step int64) (int64, error) {
	if step <= 0 {
		return 0, ErrInvalidStep
	}
	q, r := ts/step, ts%step
	if r != 0 && ts < 0 {
		q--
	}
	return q, nil
}

// Contains 判断桶 [start, start+step) 是否包含时间戳 ts。
// 恰好等于 start+step 的点不属于本桶（左闭右开）。
func Contains(start, step, ts int64) bool {
	return start <= ts && ts < start+step
}
