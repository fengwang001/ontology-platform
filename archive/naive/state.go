package naive

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Snapshot 与 archive.Service.Snapshot 使用完全一致的文本规范，便于逐字节比对。
func (m *Model) Snapshot() string {
	var b strings.Builder
	b.WriteString("now=" + strconv.Itoa(m.lastNow) + "\n")
	ids := make([]string, 0, len(m.volumes))
	for id := range m.volumes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := m.volumes[id]
		fmt.Fprintf(&b, "V %s class=%s status=%s sealPending=%t", id, v.class, v.status, v.sealPending)
		if v.loan != nil {
			fmt.Fprintf(&b, " loan=%s[%d..%d]x%d", v.loan.user,
				v.loan.start, v.loan.due, v.loan.renewals)
		}
		if v.hold != nil {
			fmt.Fprintf(&b, " hold=%s[%d..%d]", v.hold.user, v.hold.assigned, v.hold.deadline)
		}
		fmt.Fprintf(&b, " queue=%v\n", v.queue)
	}
	uids := make([]string, 0, len(m.users))
	for id := range m.users {
		uids = append(uids, id)
	}
	sort.Strings(uids)
	for _, id := range uids {
		u := m.users[id]
		fmt.Fprintf(&b, "U %s max=%s status=%s overdue=%d lastReturn=%d loans=%d\n",
			id, u.maxClass, u.status, u.overdueTotal, u.lastReturn, u.activeLoans)
	}
	return b.String()
}
