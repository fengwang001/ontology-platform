package margin

func sameADL(a, b []ADLItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameItem(a, b LiquidationItem) bool {
	return a.Account == b.Account && a.E == b.E && a.Fine == b.Fine &&
		a.Absorbed == b.Absorbed && a.BadDebt == b.BadDebt && sameADL(a.ADL, b.ADL)
}
