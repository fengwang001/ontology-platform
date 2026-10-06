package contacttracing

// nonEmptyID 校验非空字符串标识。
func nonEmptyID(ids ...string) bool {
	for _, id := range ids {
		if id == "" {
			return false
		}
	}
	return true
}

// validTime 校验时刻落在允许范围内。
func validTime(t int64) bool { return t >= 0 && t <= 10_000_000 }

// overlapLen 返回左闭右开区间 [aL,aR) 与 [bL,bR) 的正重叠长度。
func overlapLen(aL, aR, bL, bR int64) int64 {
	l := aL
	if bL > l {
		l = bL
	}
	r := aR
	if bR < r {
		r = bR
	}
	if r <= l {
		return 0
	}
	return r - l
}
