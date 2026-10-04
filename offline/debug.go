package offline

import (
	"fmt"
	"sort"
	"strings"
)

// DebugSummary returns a deterministic snapshot of all accounts, devices,
// cooldown slots, licenses and titles. It is intended for differential tests.
func (m *Manager) DebugSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	accts := m.devices.Accounts()
	sort.Strings(accts)
	for _, a := range accts {
		devs := m.devices.Devices(a)
		sort.Strings(devs)
		fmt.Fprintf(&b, "A %s D %v\n", a, devs)
		type coolRow struct {
			until int64
			dev   string
		}
		var cools []coolRow
		for _, c := range m.devices.Cooldowns(a) {
			cools = append(cools, coolRow{c[0].(int64), c[1].(string)})
		}
		sort.Slice(cools, func(i, j int) bool {
			if cools[i].until != cools[j].until {
				return cools[i].until < cools[j].until
			}
			return cools[i].dev < cools[j].dev
		})
		for _, c := range cools {
			fmt.Fprintf(&b, "  C %s@%d\n", c.dev, c.until)
		}
		recs := m.licenses.AllRecs(a)
		sort.Slice(recs, func(i, j int) bool {
			if recs[i].Dev != recs[j].Dev {
				return recs[i].Dev < recs[j].Dev
			}
			return recs[i].Title < recs[j].Title
		})
		for _, f := range recs {
			fmt.Fprintf(&b, "  L %s %s rental=%d played=%v fp=%d\n",
				f.Dev, f.Title, f.Rec.RentalEnd, f.Rec.Played, f.Rec.FirstPlay)
		}
	}
	titles, ends := m.licenses.TitleSnapshot()
	sort.Strings(titles)
	for _, tn := range titles {
		fmt.Fprintf(&b, "T %s end=%d\n", tn, ends[tn])
	}
	return b.String()
}
