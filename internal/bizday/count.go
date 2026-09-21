package bizday

import "fmt"

// CountBusinessDays 统计左闭右开区间 [from, to) 内的工作日天数。
//
//   - CountBusinessDays(d, d) 恒为 0，to 当天不计入；
//   - from > to 时返回包裹 ErrInvalidRange 的错误，不返回负数；
//   - from 或 to 非法时返回包裹 ErrInvalidDate 的错误。
func (c *Calendar) CountBusinessDays(from, to int) (int, error) {
	if err := validateDate(from); err != nil {
		return 0, err
	}
	if err := validateDate(to); err != nil {
		return 0, err
	}
	if from > to {
		return 0, fmt.Errorf("%w: from=%d to=%d", ErrInvalidRange, from, to)
	}

	count := 0
	for d := from; d < to; d = shiftDay(d, 1) {
		if c.isBusinessDay(d) {
			count++
		}
	}
	return count, nil
}
