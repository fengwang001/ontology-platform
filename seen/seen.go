package seen

type Table struct {
	n      int
	floors [][]int64
}

func New(n int) *Table {
	floors := make([][]int64, n+1)
	for site := 1; site <= n; site++ {
		floors[site] = make([]int64, n+1)
	}
	return &Table{n: n, floors: floors}
}

func (t *Table) Floor(site, origin int) int64 {
	return t.floors[site][origin]
}

func (t *Table) Set(site, origin int, seq int64) {
	t.floors[site][origin] = seq
}

func (t *Table) Observe(site, origin int, seq int64) bool {
	floor := t.floors[site][origin]
	if seq <= floor {
		return false
	}
	if seq != floor+1 {
		panic("seen: change sequence gap")
	}
	t.floors[site][origin] = seq
	return true
}
