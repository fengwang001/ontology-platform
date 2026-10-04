package approval

import "sort"

type Ticket struct {
	User string
	At   int64
	TTL  int64
}

func (t Ticket) Valid(at int64) bool {
	return at < t.At+t.TTL
}

type Ledger struct {
	tickets map[string]Ticket
}

func NewLedger() *Ledger {
	return &Ledger{tickets: make(map[string]Ticket)}
}

func (l *Ledger) Cast(user string, at int64, ttl int64) (duplicate bool) {
	current, ok := l.tickets[user]
	if ok && current.Valid(at) {
		return true
	}
	l.tickets[user] = Ticket{User: user, At: at, TTL: ttl}
	return false
}

func (l *Ledger) ValidCount(at int64) int {
	count := 0
	for _, ticket := range l.tickets {
		if ticket.Valid(at) {
			count++
		}
	}
	return count
}

func (l *Ledger) Tickets() []Ticket {
	tickets := make([]Ticket, 0, len(l.tickets))
	for _, ticket := range l.tickets {
		tickets = append(tickets, ticket)
	}
	sort.Slice(tickets, func(i, j int) bool {
		if tickets[i].At != tickets[j].At {
			return tickets[i].At < tickets[j].At
		}
		return tickets[i].User < tickets[j].User
	})
	return tickets
}
