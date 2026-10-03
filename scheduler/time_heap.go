package scheduler

const minutesPerDay = 1440

func nonNegativeModulo(value int64, modulus int64) int64 {
	return ((value % modulus) + modulus) % modulus
}

func localMinute(t int64, off int) int64 {
	return nonNegativeModulo(t+int64(off), minutesPerDay)
}

func localDay(t int64, off int) int64 {
	return floorDiv(t+int64(off), minutesPerDay)
}

func floorDiv(value int64, divisor int64) int64 {
	quotient := value / divisor
	if remainder := value % divisor; remainder != 0 && (value < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}

func (s *Scheduler) inQuiet(t int64) bool {
	lm := localMinute(t, s.off)
	if s.qs < s.qe {
		return int64(s.qs) <= lm && lm < int64(s.qe)
	}
	if s.qs > s.qe {
		return lm >= int64(s.qs) || lm < int64(s.qe)
	}
	return false
}

func (s *Scheduler) shift(t int64) int64 {
	if !s.inQuiet(t) {
		return t
	}
	lm := localMinute(t, s.off)
	wait := nonNegativeModulo(int64(s.qe)-lm, minutesPerDay)
	return t + wait
}

func (s *Scheduler) nextDayStart(day int64) int64 {
	return (day+1)*minutesPerDay - int64(s.off)
}
