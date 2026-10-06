package submission

// Extension 一次延期授权。StudentID 非空为个人延期，否则为小组延期。
type Extension struct {
	ID           string
	AssignmentID string
	StudentID    string
	GroupID      string
	Duration     int // 延长时长
	GrantedAt    int
	Revoked      bool
}

// grantPersonal 记录个人延期并维护该学生的最大延长时长缓存。
func (a *Assignment) grantPersonal(ext *Extension) {
	a.personalExts[ext.StudentID] = append(a.personalExts[ext.StudentID], ext)
	if ext.Duration > a.personalMax[ext.StudentID] {
		a.personalMax[ext.StudentID] = ext.Duration
	}
}

// grantGroup 记录小组延期并维护该小组的最大延长时长缓存。
func (a *Assignment) grantGroup(ext *Extension) {
	a.groupExts[ext.GroupID] = append(a.groupExts[ext.GroupID], ext)
	if ext.Duration > a.groupMax[ext.GroupID] {
		a.groupMax[ext.GroupID] = ext.Duration
	}
}

// revoke 撤销延期，只影响此后的版本；仅当被撤销的是当前最大值时
// 才重算该学生/小组的缓存，重算开销只随该主体的延期数增长。
func (a *Assignment) revoke(ext *Extension) {
	ext.Revoked = true
	if ext.StudentID != "" {
		if a.personalMax[ext.StudentID] == ext.Duration {
			a.personalMax[ext.StudentID] = maxActive(a.personalExts[ext.StudentID])
		}
		return
	}
	if a.groupMax[ext.GroupID] == ext.Duration {
		a.groupMax[ext.GroupID] = maxActive(a.groupExts[ext.GroupID])
	}
}

func maxActive(exts []*Extension) int {
	m := 0
	for _, e := range exts {
		if !e.Revoked && e.Duration > m {
			m = e.Duration
		}
	}
	return m
}
