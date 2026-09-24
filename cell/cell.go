package cell

type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
	Record int
	Field  int
}
