package cell

// Cell describes one CSV field and its original byte span.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}
