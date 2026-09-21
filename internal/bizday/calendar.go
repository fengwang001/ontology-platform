package bizday

// Calendar 是工作日历。
//
// 工作日判定优先级（高到低）：
//  1. 在 holidays 中：不是工作日；
//  2. 在 workdays 中：是工作日（调休上班）；
//  3. 否则周一到周五是工作日，周六周日不是。
type Calendar struct {
	holidays map[int]struct{}
	workdays map[int]struct{}
}

// New 构建工作日历。
//
// holidays 为法定节假日，workdays 为调休上班日（本是周末但要上班）。
// 两者允许重叠，重叠日按节假日处理；New 不去重也不因此报错。
// 任一日期非法时返回包裹 ErrInvalidDate 的错误。
func New(holidays []int, workdays []int) (*Calendar, error) {
	c := &Calendar{
		holidays: make(map[int]struct{}, len(holidays)),
		workdays: make(map[int]struct{}, len(workdays)),
	}
	for _, d := range holidays {
		if err := validateDate(d); err != nil {
			return nil, err
		}
		c.holidays[d] = struct{}{}
	}
	for _, d := range workdays {
		if err := validateDate(d); err != nil {
			return nil, err
		}
		c.workdays[d] = struct{}{}
	}
	return c, nil
}

// IsBusinessDay 判定 d 是否为工作日。
// d 非法时返回包裹 ErrInvalidDate 的错误。
func (c *Calendar) IsBusinessDay(d int) (bool, error) {
	if err := validateDate(d); err != nil {
		return false, err
	}
	return c.isBusinessDay(d), nil
}

// isBusinessDay 是 IsBusinessDay 的内部版本，假定 d 已校验合法。
func (c *Calendar) isBusinessDay(d int) bool {
	if _, ok := c.holidays[d]; ok {
		return false
	}
	if _, ok := c.workdays[d]; ok {
		return true
	}
	return weekday(d) < 5
}
