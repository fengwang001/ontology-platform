package ontology

// group 记录小组的当前成员与已退出成员。
// 退出成员的结算以退出时刻小组的最新版本为准，此后的新版本与其无关。
type group struct {
	id      string
	members map[string]bool // 当前在组内的成员
	former  map[string]int  // 已退出成员 -> 退出时刻小组的最新版本号（0 表示退出时尚无版本）
}

func newGroup(id string) *group {
	return &group{
		id:      id,
		members: map[string]bool{},
		former:  map[string]int{},
	}
}
