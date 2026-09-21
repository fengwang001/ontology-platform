package bizday

const (
	minYear = 1900
	maxYear = 2999
)

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func daysInMonth(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}

func split(d int) (y, m, day int) {
	return d / 10000, d / 100 % 100, d % 100
}

func join(y, m, day int) int {
	return y*10000 + m*100 + day
}

func validDate(d int) bool {
	y, m, day := split(d)
	if y < minYear || y > maxYear || m < 1 || m > 12 {
		return false
	}
	return day >= 1 && day <= daysInMonth(y, m)
}

// toSerial 返回自 1900-01-01 起的天数偏移（当天为 0）。
func toSerial(y, m, day int) int {
	s := 0
	for yy := minYear; yy < y; yy++ {
		s += 365
		if isLeap(yy) {
			s++
		}
	}
	for mm := 1; mm < m; mm++ {
		s += daysInMonth(y, mm)
	}
	return s + day - 1
}

// fromSerial 是 toSerial 的逆运算；s 越界时结果由调用方用 validDate 判定。
func fromSerial(s int) (y, m, day int) {
	y = minYear
	for s >= 0 {
		dy := 365
		if isLeap(y) {
			dy = 366
		}
		if s < dy {
			break
		}
		s -= dy
		y++
	}
	m = 1
	for s >= 0 && m <= 12 {
		dm := daysInMonth(y, m)
		if s < dm {
			break
		}
		s -= dm
		m++
	}
	return y, m, s + 1
}

// weekday 返回星期几：0=周日，1=周一，…，6=周六。
// 已知 1900-01-01 是周一。
func weekday(d int) int {
	y, m, day := split(d)
	return (toSerial(y, m, day) + 1) % 7
}
