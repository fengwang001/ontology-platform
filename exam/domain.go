package exam

import (
	"fmt"
	"sort"
)

// VenueRegistry 维护考场及容量。
type VenueRegistry struct {
	cap map[string]int
}

// NewVenueRegistry 构造空考场登记表。
func NewVenueRegistry() *VenueRegistry {
	return &VenueRegistry{cap: map[string]int{}}
}

// Add 登记一个考场及其容量；容量须为正，重名登记须与原容量一致。
func (v *VenueRegistry) Add(id string, capacity int) error {
	if id == "" {
		return fmt.Errorf("empty room id")
	}
	if capacity <= 0 {
		return fmt.Errorf("invalid capacity %d for room %q", capacity, id)
	}
	if old, ok := v.cap[id]; ok && old != capacity {
		return fmt.Errorf("room %q re-registered with different capacity %d != %d", id, capacity, old)
	}
	v.cap[id] = capacity
	return nil
}

// Has 判断考场是否已登记。
func (v *VenueRegistry) Has(id string) bool {
	_, ok := v.cap[id]
	return ok
}

// Capacity 返回考场容量；不存在返回 -1。
func (v *VenueRegistry) Capacity(id string) int {
	if c, ok := v.cap[id]; ok {
		return c
	}
	return -1
}

// SortedIDs 返回按编号升序的全部考场。
func (v *VenueRegistry) SortedIDs() []string {
	ids := make([]string, 0, len(v.cap))
	for id := range v.cap {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Exam 是一门考试的静态信息。
// StandardSlots 为标准时长（整时段数）；允许加时时，延长量按
// RatioNum/RatioDen 的比例作用于标准时长，不足一个时段向上进位。
type Exam struct {
	ID            string
	Students      []string
	Extended      map[string]bool
	StandardSlots int
	AllowExtended bool
	RatioNum      int
	RatioDen      int
}

// Enrollment 维护考试与学生选考关系。
type Enrollment struct {
	exams map[string]*examInfo
}

type examInfo struct {
	id            string
	students      []string
	extended      map[string]bool
	standardSlots int
	allowExtended bool
	ratioNum      int
	ratioDen      int
}

// NewEnrollment 构造选考登记表。
func NewEnrollment() *Enrollment {
	return &Enrollment{exams: map[string]*examInfo{}}
}

// AddExam 登记一门考试。
// 非法情形：编号为空、无学生、标准时长非正、比例非法、延长学生不在名单、学生重复。
func (e *Enrollment) AddExam(x *Exam) error {
	if x == nil {
		return fmt.Errorf("nil exam")
	}
	if x.ID == "" {
		return fmt.Errorf("empty exam id")
	}
	if _, dup := e.exams[x.ID]; dup {
		return fmt.Errorf("duplicate exam %q", x.ID)
	}
	if x.StandardSlots <= 0 {
		return fmt.Errorf("exam %q has non-positive standard slots", x.ID)
	}
	if len(x.Students) == 0 {
		return fmt.Errorf("exam %q has no students", x.ID)
	}
	if x.AllowExtended {
		if x.RatioNum <= 0 || x.RatioDen <= 0 || x.RatioNum > x.RatioDen {
			return fmt.Errorf("exam %q has invalid extension ratio %d/%d", x.ID, x.RatioNum, x.RatioDen)
		}
	}
	seen := map[string]bool{}
	for _, st := range x.Students {
		if st == "" {
			return fmt.Errorf("exam %q has empty student id", x.ID)
		}
		if seen[st] {
			return fmt.Errorf("exam %q has duplicate student %q", x.ID, st)
		}
		seen[st] = true
	}
	for st := range x.Extended {
		if !seen[st] {
			return fmt.Errorf("extended student %q not enrolled in exam %q", st, x.ID)
		}
	}
	students := append([]string(nil), x.Students...)
	sort.Strings(students)
	extended := map[string]bool{}
	for st, v := range x.Extended {
		extended[st] = v
	}
	e.exams[x.ID] = &examInfo{
		id:            x.ID,
		students:      students,
		extended:      extended,
		standardSlots: x.StandardSlots,
		allowExtended: x.AllowExtended,
		ratioNum:      x.RatioNum,
		ratioDen:      x.RatioDen,
	}
	return nil
}

// Get 返回考试静态信息，不存在返回 nil。
func (e *Enrollment) Get(id string) *examInfo { return e.exams[id] }

// DurationSlots 返回该考试实际占用的时段数（取所有参考学生中最长者）。
func (x *examInfo) DurationSlots() int {
	extra := 0
	if x.allowExtended && len(x.extended) > 0 {
		// 延长量 = ceil(standard * num / den)
		extra = (x.standardSlots*x.ratioNum + x.ratioDen - 1) / x.ratioDen
	}
	return x.standardSlots + extra
}

// StudentEnd 返回某学生参加该考试时其个人占用区间的右端（闭）。
// 延长学生使用延长后的终点，其余学生使用标准终点。
func (x *examInfo) StudentEnd(start int, student string) int {
	end := start + x.standardSlots - 1
	if x.allowExtended && x.extended[student] {
		return start + x.DurationSlots() - 1
	}
	return end
}

// StaffRegistry 维护可用监考人员名单。
type StaffRegistry struct {
	set map[string]bool
}

// NewStaffRegistry 构造监考人员登记表。
func NewStaffRegistry() *StaffRegistry {
	return &StaffRegistry{set: map[string]bool{}}
}

// Add 登记一名监考人员；重复登记忽略。
func (s *StaffRegistry) Add(id string) {
	if id != "" {
		s.set[id] = true
	}
}

// Has 判断监考人员是否可用。
func (s *StaffRegistry) Has(id string) bool { return s.set[id] }

// List 返回按编号升序的全部监考人员，作为确定性选取的唯一顺序来源。
func (s *StaffRegistry) List() []string {
	out := make([]string, 0, len(s.set))
	for id := range s.set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
