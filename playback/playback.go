// Package playback handles first-play timing, expiry decisions and read-only status.
package playback

import "ontology/license"
import "ontology/errs"

// Status values.
const (
	StatusUnplayed = "unplayed"
	StatusPlaying  = "playing"
	StatusExpired  = "expired"
)

// Result is the read-only status of one license.
type Result struct {
	Status string
	Exp    int64
	Reason string // empty unless Status == StatusExpired
}

// Service performs playback operations against a license store.
type Service struct{ lics *license.Store }

// NewService creates a playback service.
func NewService(lics *license.Store) *Service { return &Service{lics: lics} }

// Play allows playback at now, recording firstPlay on the first allowed play.
// Play never extends any deadline. The caller guarantees existence checks.
func (p *Service) Play(now int64, acct, dev, title string) error {
	r := p.lics.Get(acct, dev, title)
	p.lics.Touched++ // exactly the one located record
	if r == nil {
		return errs.ErrNoLicense
	}
	if !p.lics.IsValid(r, title, now) {
		return p.lics.ErrExpired(r, title, now)
	}
	if !r.Played {
		r.Played = true
		r.FirstPlay = now
	}
	return nil
}

// Status returns the read-only status at now. Missing license is reported by ok=false.
func (p *Service) Status(acct, dev, title string, now int64) (Result, bool) {
	r := p.lics.Get(acct, dev, title)
	if r == nil {
		return Result{}, false
	}
	exp := p.lics.Exp(r, title)
	if now >= exp {
		return Result{Status: StatusExpired, Exp: exp, Reason: p.lics.ErrExpired(r, title, now).Reason}, true
	}
	if r.Played {
		return Result{Status: StatusPlaying, Exp: exp}, true
	}
	return Result{Status: StatusUnplayed, Exp: exp}, true
}
