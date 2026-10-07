package ontology

// Metrics 是单次请求实际访问规模的内部度量，不参与语义正确性。
type Metrics struct {
	ObjectsVisited int
	LinksExamined  int
}
