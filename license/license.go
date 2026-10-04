// Package license issues, renews and counts offline licenses.
package license

import "ontology/errs"

// DeviceChecker is implemented by the device store (interface inversion avoids an import cycle).
type DeviceChecker interface {
	IsRegistered(acct, dev string) bool
}

// Rec is one offline license record for (device, title).
type Rec struct {
	RentalEnd int64
	FirstPlay int64 // 0 == never played; all real timestamps are >= 0, 0 is also a legal now
	Played    bool
}

type title struct{ end int64 }

type devLic map[string]*Rec // title -> record
type acctLic map[string]devLic

// Store holds the title catalog and all licenses.
type Store struct {
	lr, lp  int64
	omax    int
	devices DeviceChecker
	titles  map[string]*title
	lics    map[string]acctLic
	Touched int
}

// NewStore creates a license store.
func NewStore(lr, lp int64, omax int, devices DeviceChecker) *Store {
	return &Store{lr: lr, lp: lp, omax: omax, devices: devices, titles: map[string]*title{}, lics: map[string]acctLic{}}
}

// AddTitle registers a title with its takedown time.
func (s *Store) AddTitle(t string, end int64) error {
	if _, ok := s.titles[t]; ok {
		return errs.ErrTitleExists
	}
	s.titles[t] = &title{end: end}
	return nil
}

// HasTitle reports whether the title exists.
func (s *Store) HasTitle(t string) bool { _, ok := s.titles[t]; return ok }

// TitleEnd returns the current takedown time of a title.
func (s *Store) TitleEnd(t string) (int64, bool) {
	ti, ok := s.titles[t]
	if !ok {
		return 0, false
	}
	return ti.end, true
}

// TitleSnapshot returns title names and a map of their current takedown times.
func (s *Store) TitleSnapshot() ([]string, map[string]int64) {
	names := make([]string, 0, len(s.titles))
	ends := make(map[string]int64, len(s.titles))
	for t, ti := range s.titles {
		names = append(names, t)
		ends[t] = ti.end
	}
	return names, ends
}

// SetTitleEnd moves the takedown time (earlier or later).
func (s *Store) SetTitleEnd(now int64, t string, end int64) error {
	ti, ok := s.titles[t]
	if !ok {
		return errs.ErrNoTitle
	}
	if end <= now {
		return errs.ErrInvalidParam
	}
	ti.end = end
	return nil
}

// Download issues or renews a license at now. Caller guarantees account/title
// existence, device registration and title availability have been checked in the
// required rejection order.
func (s *Store) Download(now int64, acct, dev, titleName string) error {
	ti := s.titles[titleName]
	devMap, ok := s.lics[acct]
	if !ok {
		devMap = acctLic{}
		s.lics[acct] = devMap
	}
	titlesMap, ok := devMap[dev]
	if !ok {
		titlesMap = devLic{}
		devMap[dev] = titlesMap
	}
	if r, ok := titlesMap[titleName]; ok {
		s.Touched++
		if r.Played {
			if now < s.Exp(r, titleName) {
				return errs.ErrAlreadyPlayed
			}
		} else if now < s.expUnplayed(r, ti.end) {
			r.RentalEnd = now + s.lr // renewal: no new slot consumed
			return nil
		}
		// Expired: treated as a brand-new issuance, replacing the old record.
	}
	// Count currently-valid licenses of this account. Every record under the
	// account is touched at most once; other accounts are never examined.
	valid := 0
	for _, tms := range devMap {
		for tn, r := range tms {
			s.Touched++
			if now < s.Exp(r, tn) {
				valid++
			}
		}
	}
	s.Touched++ // the new record itself
	if valid >= s.omax {
		return errs.ErrLicenseFull
	}
	titlesMap[titleName] = &Rec{RentalEnd: now + s.lr}
	return nil
}

// DeleteDeviceLicenses removes every license owned by dev under acct.
func (s *Store) DeleteDeviceLicenses(acct, dev string) {
	if devMap, ok := s.lics[acct]; ok {
		delete(devMap, dev)
	}
}

// Get returns the license record for (acct, dev, title), or nil.
func (s *Store) Get(acct, dev, titleName string) *Rec {
	if devMap, ok := s.lics[acct]; ok {
		if titlesMap, ok := devMap[dev]; ok {
			return titlesMap[titleName]
		}
	}
	return nil
}

// Exp returns the current expiry timestamp of r for titleName. The takedown
// time is read live: before first play it is min(rentalEnd, titleEnd); after
// first play it is min(firstPlay+Lp, titleEnd) and rentalEnd no longer applies.
func (s *Store) Exp(r *Rec, titleName string) int64 {
	ti := s.titles[titleName]
	if r.Played {
		playEnd := r.FirstPlay + s.lp
		if playEnd > ti.end {
			return ti.end
		}
		return playEnd
	}
	return s.expUnplayed(r, ti.end)
}

func (s *Store) expUnplayed(r *Rec, titleEnd int64) int64 {
	if r.RentalEnd > titleEnd {
		return titleEnd
	}
	return r.RentalEnd
}

// IsValid reports whether r is valid at now.
func (s *Store) IsValid(r *Rec, titleName string, now int64) bool {
	return now < s.Exp(r, titleName)
}

// AllRecs returns a snapshot of every license of acct as (dev, title, Rec), for simulators/tests.
func (s *Store) AllRecs(acct string) []struct {
	Dev, Title string
	Rec        Rec
} {
	out := []struct {
		Dev, Title string
		Rec        Rec
	}{}
	for dev, tms := range s.lics[acct] {
		for tn, r := range tms {
			out = append(out, struct {
				Dev, Title string
				Rec        Rec
			}{dev, tn, *r})
		}
	}
	return out
}

// ErrExpired builds an *errs.ExpiredError for r at now. Reason precedence:
// takedown > playback window ended (after first play) > rental ended (unplayed).
func (s *Store) ErrExpired(r *Rec, titleName string, now int64) *errs.ExpiredError {
	exp := s.Exp(r, titleName)
	ti := s.titles[titleName]
	switch {
	case now >= ti.end:
		return &errs.ExpiredError{Reason: errs.ReasonTitleEnded, Exp: exp}
	case r.Played && now >= r.FirstPlay+s.lp:
		return &errs.ExpiredError{Reason: errs.ReasonPlayEnded, Exp: exp}
	default:
		return &errs.ExpiredError{Reason: errs.ReasonRentalEnded, Exp: exp}
	}
}
