package cell

type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}
