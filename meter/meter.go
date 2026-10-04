package meter

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockSkew       = errors.New("clock skew")
)

type Kind uint8

const (
	Quota Kind = iota + 1
	Gift
)

type Record struct {
	Article     string
	FirstUnlock int64
	Kind        Kind
}

type Ledger struct {
	maxNow   int64
	monthLen int64
	quota    int
	books    map[string]map[int64]*book
	touched  int
}

type book struct {
	records map[string]Record
	quota   int
	touched int
}

func New(quota int, monthLen int64) *Ledger {
	if quota < 0 || quota > 1000 || monthLen < 1 || monthLen > 1_000_000_000 {
		panic(ErrInvalidArgument)
	}
	return &Ledger{monthLen: monthLen, quota: quota, books: map[string]map[int64]*book{}}
}

func (l *Ledger) Advance(now int64) error {
	if err := l.Check(now); err != nil {
		return err
	}
	l.maxNow = now
	return nil
}

func (l *Ledger) Check(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if now < l.maxNow {
		return ErrClockSkew
	}
	return nil
}

func (l *Ledger) Month(now int64) int64 {
	return now / l.monthLen
}

func (l *Ledger) Used(subject string, now int64) int {
	current := l.currentBook(subject, now)
	if current == nil {
		return 0
	}
	return current.quota
}

func (l *Ledger) Lookup(subject string, now int64, article string) (Record, bool) {
	current := l.currentBook(subject, now)
	if current == nil {
		return Record{}, false
	}
	current.touched = 0
	record, ok := current.records[article]
	current.touched++
	return record, ok
}

func (l *Ledger) AddQuota(subject string, now int64, article string) {
	current := l.ensureBook(subject, l.Month(now))
	if _, ok := current.records[article]; ok {
		current.touched++
		return
	}
	current.records[article] = Record{Article: article, FirstUnlock: now, Kind: Quota}
	current.quota++
	current.touched++
}

func (l *Ledger) AddGift(subject string, now int64, article string) {
	current := l.ensureBook(subject, l.Month(now))
	if record, ok := current.records[article]; ok {
		current.touched++
		if record.Kind != Gift {
			record.Kind = Gift
			current.records[article] = record
			current.quota--
		}
		return
	}
	current.records[article] = Record{Article: article, FirstUnlock: now, Kind: Gift}
	current.touched++
}

func (l *Ledger) Merge(now int64, user, device string) int {
	month := l.Month(now)
	userBook := l.ensureBook(user, month)
	userBook.touched = 0
	deviceBook := l.currentBookAt(device, month)
	if deviceBook == nil {
		l.touched = 0
		return 0
	}

	merged := make(map[string]Record, len(userBook.records)+len(deviceBook.records))
	for _, record := range userBook.records {
		merged[record.Article] = record
	}
	for _, deviceRecord := range deviceBook.records {
		userRecord, ok := merged[deviceRecord.Article]
		if !ok {
			merged[deviceRecord.Article] = deviceRecord
			continue
		}
		if deviceRecord.FirstUnlock < userRecord.FirstUnlock {
			userRecord.FirstUnlock = deviceRecord.FirstUnlock
		}
		if deviceRecord.Kind == Gift || userRecord.Kind == Gift {
			userRecord.Kind = Gift
		}
		merged[deviceRecord.Article] = userRecord
	}

	quotas := make([]Record, 0, len(merged))
	for _, record := range merged {
		if record.Kind == Quota {
			quotas = append(quotas, record)
		}
	}
	sort.Slice(quotas, func(i, j int) bool {
		if quotas[i].FirstUnlock != quotas[j].FirstUnlock {
			return quotas[i].FirstUnlock < quotas[j].FirstUnlock
		}
		return quotas[i].Article < quotas[j].Article
	})

	resultQuota := 0
	for i, record := range quotas {
		if i < l.quota {
			resultQuota++
			continue
		}
		delete(merged, record.Article)
	}

	result := &book{
		records: merged,
		quota:   resultQuota,
		touched: len(userBook.records) + len(deviceBook.records),
	}
	l.books[user][month] = result
	l.touched = result.touched
	return resultQuota
}

func (l *Ledger) Touched(subject string, now int64) int {
	if subject == "" {
		return l.touched
	}
	current := l.currentBook(subject, now)
	if current == nil {
		return 0
	}
	return current.touched
}

func (l *Ledger) ResetTouched() {
	// Used by tests that need an exact per-operation touch budget.
	for _, months := range l.books {
		for _, current := range months {
			current.touched = 0
		}
	}
	l.touched = 0
}

func (l *Ledger) currentBook(subject string, now int64) *book {
	return l.currentBookAt(subject, l.Month(now))
}

func (l *Ledger) currentBookAt(subject string, month int64) *book {
	if months, ok := l.books[subject]; ok {
		return months[month]
	}
	return nil
}

func (l *Ledger) ensureBook(subject string, month int64) *book {
	months, ok := l.books[subject]
	if !ok {
		months = map[int64]*book{}
		l.books[subject] = months
	}
	current, ok := months[month]
	if !ok {
		current = &book{records: map[string]Record{}}
		months[month] = current
	}
	return current
}
