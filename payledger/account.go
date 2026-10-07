package payledger

// Account holds the ledger state of one card account.
//
// Available credit is defined as creditLimit - posted - activeHolds(now),
// where activeHolds only counts holds whose expiryDay >= now. Expiry is
// lazy: holds are grouped per expiry day in buckets, and totalHolds always
// equals the sum of buckets (all of which have day >= sweepDay). Computing
// the available credit at any now >= sweepDay is a pure function that only
// needs to subtract the buckets expiring in [sweepDay, now).
type Account struct {
	id          string
	creditLimit int64
	posted      int64
	totalHolds  int64
	sweepDay    int64
	buckets     map[int64]int64 // expiryDay -> remaining hold expiring that day
	scanSteps   int64           // instrumentation: bucket/day scan steps (proof aid)
}

func newAccount(id string, creditLimit int64, now int64) *Account {
	return &Account{
		id:          id,
		creditLimit: creditLimit,
		sweepDay:    now,
		buckets:     make(map[int64]int64),
	}
}

// expiredBetween returns the total hold amount expiring on days in
// [from, to). It is read-only and costs O(min(to-from, len(buckets))):
// independent of the account's historical authorization count and of the
// total number of accounts.
func (a *Account) expiredBetween(from, to int64) int64 {
	if to <= from {
		return 0
	}
	var expired int64
	if to-from <= int64(len(a.buckets)) {
		for d := from; d < to; d++ {
			a.scanSteps++
			expired += a.buckets[d]
		}
		return expired
	}
	for day, amount := range a.buckets {
		a.scanSteps++
		if day >= from && day < to {
			expired += amount
		}
	}
	return expired
}

// availableAt is the pure available-credit function: it depends only on the
// accepted operations and the query day, and never mutates the account.
func (a *Account) availableAt(now int64) int64 {
	active := a.totalHolds - a.expiredBetween(a.sweepDay, now)
	return a.creditLimit - a.posted - active
}

// sweep physically drops buckets expiring before now and advances sweepDay.
// It is semantics-neutral (those holds were already inactive) and only runs
// inside accepted mutating operations, so each bucket is swept at most once:
// amortized O(1) per accepted operation.
func (a *Account) sweep(now int64) {
	if now <= a.sweepDay {
		return
	}
	if now-a.sweepDay <= int64(len(a.buckets)) {
		for d := a.sweepDay; d < now; d++ {
			a.scanSteps++
			if amount, ok := a.buckets[d]; ok {
				a.totalHolds -= amount
				delete(a.buckets, d)
			}
		}
	} else {
		for day, amount := range a.buckets {
			a.scanSteps++
			if day < now {
				a.totalHolds -= amount
				delete(a.buckets, day)
			}
		}
	}
	a.sweepDay = now
}

// addHold books amount into the bucket of expiryDay.
func (a *Account) addHold(expiryDay, amount int64) {
	if amount == 0 {
		return
	}
	a.buckets[expiryDay] += amount
	a.totalHolds += amount
}

// removeHold removes amount from the bucket of expiryDay.
func (a *Account) removeHold(expiryDay, amount int64) {
	if amount == 0 {
		return
	}
	left := a.buckets[expiryDay] - amount
	if left == 0 {
		delete(a.buckets, expiryDay)
	} else {
		a.buckets[expiryDay] = left
	}
	a.totalHolds -= amount
}
