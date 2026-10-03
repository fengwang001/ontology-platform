package ledger

import "slices"

type Status string

const (
	StatusSuccess Status = "S"
	StatusUndone  Status = "U"
	StatusFailed  Status = "F"
)

type Row struct {
	Rank   int64
	Ver    int64
	Sum    uint64
	Status Status
}

type Ledger struct {
	nextRank int64
	rows     []Row
}

func New() *Ledger {
	return &Ledger{nextRank: 1}
}

func (l *Ledger) Append(ver int64, sum uint64, status Status) Row {
	row := Row{
		Rank:   l.nextRank,
		Ver:    ver,
		Sum:    sum,
		Status: status,
	}
	l.rows = append(l.rows, row)
	l.nextRank++
	return row
}

func (l *Ledger) Rows() []Row {
	return slices.Clone(l.rows)
}

func (l *Ledger) RemoveFailed() int {
	kept := l.rows[:0]
	removed := 0
	for _, row := range l.rows {
		if row.Status == StatusFailed {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	l.rows = kept
	return removed
}

func (l *Ledger) SetStatus(rank int64, status Status) bool {
	for i := range l.rows {
		if l.rows[i].Rank == rank {
			l.rows[i].Status = status
			return true
		}
	}
	return false
}
