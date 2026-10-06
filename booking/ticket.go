package booking

type Flight struct {
	ID       string
	Price    int64
	DepartAt int64
	Canceled bool
}

type Ticket struct {
	ID            string
	Owner         string
	FlightID      string
	Price         int64
	DepartAt      int64
	Changes       int
	PaidChangeFee int64
	Refunded      bool
	Involuntary   bool
	LastEventAt   int64
}

func (t *Ticket) alive() bool {
	return !t.Refunded
}
