package bitemporal

import "sort"

func sortEvs(evs []halfEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].rt != evs[j].rt {
			return evs[i].rt < evs[j].rt
		}
		if evs[i].vt != evs[j].vt {
			return evs[i].vt < evs[j].vt
		}
		return evs[i].side < evs[j].side
	})
}
