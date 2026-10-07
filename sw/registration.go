package sw

import "time"

type Registration struct {
	scope          string
	scriptURL      string
	pendingRemoval bool
	installing     *Version
	waiting        *Version
	active         *Version
	versions       []*Version
	controlled     int
	lastCheck      time.Time
	hasCheck       bool
}

func (r *Registration) Scope() string        { return r.scope }
func (r *Registration) ScriptURL() string    { return r.scriptURL }
func (r *Registration) PendingRemoval() bool { return r.pendingRemoval }
func (r *Registration) ControlledCount() int { return r.controlled }
func (r *Registration) Installing() *Version { return r.installing }
func (r *Registration) Waiting() *Version    { return r.waiting }
func (r *Registration) Active() *Version     { return r.active }
func (r *Registration) Versions() []*Version { return r.versions }
