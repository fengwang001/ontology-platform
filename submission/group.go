package submission

import "sort"

// LeaveRecord 成员退出时刻的快照：其个人结算以退出时刻小组最新版本为准。
type LeaveRecord struct {
	GroupID   string
	VersionNo int // 退出时刻小组的最新版本号，0 表示当时尚无版本
}

func (a *Assignment) joinGroup(groupID, studentID string) {
	if a.groups[groupID] == nil {
		a.groups[groupID] = map[string]bool{}
	}
	a.groups[groupID][studentID] = true
	a.studentGroup[studentID] = groupID
}

// leaveGroup 退出小组：记录退出时刻小组最新版本，之后小组的新版本与其无关。
func (a *Assignment) leaveGroup(studentID string) {
	groupID := a.studentGroup[studentID]
	a.leavers[studentID] = LeaveRecord{
		GroupID:   groupID,
		VersionNo: len(a.versions[groupID]),
	}
	delete(a.groups[groupID], studentID)
	delete(a.studentGroup, studentID)
}

// membersOf 返回小组成员的有序列表，保证判定与结算可复现。
func (a *Assignment) membersOf(groupID string) []string {
	members := make([]string, 0, len(a.groups[groupID]))
	for m := range a.groups[groupID] {
		members = append(members, m)
	}
	sort.Strings(members)
	return members
}
