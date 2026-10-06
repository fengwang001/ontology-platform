package charger

import "sort"

// activeOrderedLocked 按插枪先后（并列按 ID）返回在站车辆。
func (st *Station) activeOrderedLocked() []*Session {
	list := make([]*Session, 0, len(st.portSess))
	for _, s := range st.portSess {
		list = append(list, s)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].PlugAt != list[j].PlugAt {
			return list[i].PlugAt < list[j].PlugAt
		}
		return list[i].ID < list[j].ID
	})
	return list
}
