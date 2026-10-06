package specimen

import "sync"

type catalogItem struct {
	requirement CatalogRequirement
}

type item struct {
	id                  string
	applicationID       string
	patientID           string
	projectID           string
	priority            string
	status              string
	requirement         CatalogRequirement
	enteredWaitingAt    int64
	collectedAt         int64
	rejectionCount      int
	lastRejectionReason string
	tubeID              string
	activeNode          *activeNode
}

type tube struct {
	id        string
	patientID string
	tubeType  string
	method    string
	active    bool
	itemIDs   []string
}

type application struct {
	id        string
	patientID string
}

type activeNode struct {
	item *item
	prev *activeNode
	next *activeNode
}

type activeList struct {
	first *activeNode
	last  *activeNode
}

func (l *activeList) push(it *item) {
	node := &activeNode{item: it}
	it.activeNode = node
	if l.last == nil {
		l.first = node
		l.last = node
		return
	}
	node.prev = l.last
	l.last.next = node
	l.last = node
}

func (l *activeList) remove(it *item) {
	node := it.activeNode
	if node == nil {
		return
	}
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		l.first = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		l.last = node.prev
	}
	it.activeNode = nil
}

type System struct {
	mu              sync.Mutex
	now             int64
	catalog         map[string]catalogItem
	applications    map[string]*application
	itemsByID       map[string]*item
	tubesByID       map[string]*tube
	activeByPatient map[string]*activeList
	itemSequence    int64
	tubeSequence    int64
	appSequence     int64
}
