package cell

type Cell struct {
	Value   string
	Quoted  bool
	Start   int
	End     int
}

func New(value string, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}
