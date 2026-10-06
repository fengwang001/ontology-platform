package pharmacy

import "container/list"

const (
	minTime = 0
	maxTime = 10_000_000
	minQty  = 1
	maxQty  = 1_000_000
)

type drug struct {
	id          string
	packSize    int
	splittable  bool
	onHand      int
	reserved    int
	debt        int
	acceptedSeq int
	queueSeq    int
	queue       *list.List
}

type prescription struct {
	id        string
	patientID string
	issuedAt  int
	allAtOnce bool
	status    string
	lines     []*line
	order     int
}

type line struct {
	p         *prescription
	drugID    string
	demand    int
	reserved  int
	dispensed int
	owed      int
	queueElem *list.Element
}

type reservation struct {
	line     *line
	drugID   string
	quantity int
	start    int
	expireAt int
}

type reserveExpiry struct {
	id  int
	res *reservation
}

type prescriptionExpiry struct {
	p *prescription
}

type heapItem interface {
	heapTime() int
}

func (e reserveExpiry) heapTime() int {
	return e.res.expireAt
}

func (e prescriptionExpiry) heapTime() int {
	return e.p.issuedAt + 72*60 + 1
}

type timedEvent struct {
	at    int
	order int
	item  heapItem
}

func roundUp(value, packSize int) int {
	return ((value + packSize - 1) / packSize) * packSize
}

func roundDown(value, packSize int) int {
	return (value / packSize) * packSize
}

func reservationQuantity(d *drug, demand, available int) int {
	if d.splittable {
		return min(demand, available)
	}
	needed := roundUp(demand, d.packSize)
	if available >= needed {
		return needed
	}
	return roundDown(available, d.packSize)
}

func (d *drug) available() int {
	return d.onHand - d.reserved
}
