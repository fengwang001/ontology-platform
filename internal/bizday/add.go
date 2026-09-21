package bizday

import "fmt"

// AddBusinessDays 在日期 d 上加减 n 个工作日。
//
//   - n > 0：从 d 的后一天开始逐天检查，数满 n 个工作日，返回第 n 个；
//     d 自身是否为工作日不影响结果。
//   - n < 0：从 d 的前一天开始往前数，同理。
//   - n == 0：d 是工作日则原样返回；否则返回包裹 ErrNotBusinessDay 的错误，
//     不会静默挪到最近的工作日。
//
// d 非法时返回包裹 ErrInvalidDate 的错误；
// 计数过程中走出 [minYear, maxYear] 范围时同样返回该错误。
func (c *Calendar) AddBusinessDays(d int, n int) (int, error) {
	if err := validateDate(d); err != nil {
		return 0, err
	}
	if n == 0 {
		if !c.isBusinessDay(d) {
			return 0, fmt.Errorf("%w: %d", ErrNotBusinessDay, d)
		}
		return d, nil
	}

	step := 1
	remaining := n
	if n < 0 {
		step = -1
		remaining = -n
	}

	cur := d
	for remaining > 0 {
		cur = shiftDay(cur, step)
		if err := validateDate(cur); err != nil {
			return 0, err
		}
		if c.isBusinessDay(cur) {
			remaining--
		}
	}
	return cur, nil
}
