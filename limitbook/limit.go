package limitbook

import "sort"

// limitSeries 表示某一对象（成员、项目或家庭）的年度限额：
// 一个基础限额加上若干按保单年度索引的批改。批改生效日落在
// 哪个保单年度，就从哪个年度起生效；同一年度的多次批改以
// 后登记的一次为准。
type limitSeries struct {
	base   int64
	years  []int64         // 已批改的年度索引，升序
	limits map[int64]int64 // 年度索引 -> 批改后限额
}

func newLimitSeries(base int64) limitSeries {
	return limitSeries{base: base, limits: make(map[int64]int64)}
}

// at 返回年度 y 生效的限额：不超过 y 的最近一次批改，否则为基础限额。
func (s *limitSeries) at(y int64) int64 {
	i := sort.Search(len(s.years), func(i int) bool { return s.years[i] > y }) - 1
	if i < 0 {
		return s.base
	}
	return s.limits[s.years[i]]
}

// set 登记自年度 y 起生效的批改；同一年度后写覆盖先写。
func (s *limitSeries) set(y, limit int64) {
	if _, ok := s.limits[y]; ok {
		s.limits[y] = limit
		return
	}
	i := sort.Search(len(s.years), func(i int) bool { return s.years[i] >= y })
	s.years = append(s.years, 0)
	copy(s.years[i+1:], s.years[i:])
	s.years[i] = y
	s.limits[y] = limit
}
