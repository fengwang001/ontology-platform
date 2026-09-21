package bizday

import "fmt"

// Calendar 是工作日日历。holidays 优先级高于 workdays。
type Calendar struct {
	holidays map[int]struct{}
	workdays map[int]struct{}
}

// New 构建日历。holidays 为法定节假日，workdays 为调休上班日。
// 两者允许重叠（重叠日按非工作日处理），不做去重校验。
func New(holidays, workdays []int) (*Calendar, error) {
	c := &Calendar{
		holidays: make(map[int]struct{}, len(holidays)),
		workdays: make(map[int]struct{}, len(workdays)),
	}
	for _, d := range holidays {
		if !validDate(d) {
			return nil, fmt.Errorf("%w: %d", ErrInvalidDate, d)
		}
		c.holidays[d] = struct{}{}
	}
	for _, d := range workdays {
		if !validDate(d) {
			return nil, fmt.Errorf("%w: %d", ErrInvalidDate, d)
		}
		c.workdays[d] = struct{}{}
	}
	return c, nil
}

// IsBusinessDay 判定 d 是否为工作日：节假日 > 调休上班日 > 周一至周五。
func (c *Calendar) IsBusinessDay(d int) (bool, error) {
	if !validDate(d) {
		return false, fmt.Errorf("%w: %d", ErrInvalidDate, d)
	}
	if _, ok := c.holidays[d]; ok {
		return false, nil
	}
	if _, ok := c.workdays[d]; ok {
		return true, nil
	}
	w := weekday(d)
	return w >= 1 && w <= 5, nil
}

// AddBusinessDays 从 d 的后一天（n>0）或前一天（n<0）起数满 |n| 个工作日。
// n==0 时 d 是工作日则原样返回，否则返回 ErrNotBusinessDay。
func (c *Calendar) AddBusinessDays(d int, n int) (int, error) {
	if !validDate(d) {
		return 0, fmt.Errorf("%w: %d", ErrInvalidDate, d)
	}
	if n == 0 {
		ok, err := c.IsBusinessDay(d)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, fmt.Errorf("%w: %d", ErrNotBusinessDay, d)
		}
		return d, nil
	}
	step := 1
	if n < 0 {
		step = -1
		n = -n
	}
	y, m, day := split(d)
	s := toSerial(y, m, day)
	for n > 0 {
		s += step
		cur := join(fromSerial(s))
		if !validDate(cur) {
			return 0, fmt.Errorf("%w: %d", ErrInvalidDate, cur)
		}
		ok, err := c.IsBusinessDay(cur)
		if err != nil {
			return 0, err
		}
		if ok {
			n--
		}
	}
	return join(fromSerial(s)), nil
}

// CountBusinessDays 统计左闭右开区间 [from, to) 内的工作日数。
func (c *Calendar) CountBusinessDays(from, to int) (int, error) {
	if !validDate(from) {
		return 0, fmt.Errorf("%w: %d", ErrInvalidDate, from)
	}
	if !validDate(to) {
		return 0, fmt.Errorf("%w: %d", ErrInvalidDate, to)
	}
	if from > to {
		return 0, fmt.Errorf("%w: %d > %d", ErrInvalidRange, from, to)
	}
	fy, fm, fd := split(from)
	ty, tm, td := split(to)
	start, end := toSerial(fy, fm, fd), toSerial(ty, tm, td)
	count := 0
	for s := start; s < end; s++ {
		ok, err := c.IsBusinessDay(join(fromSerial(s)))
		if err != nil {
			return 0, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}
