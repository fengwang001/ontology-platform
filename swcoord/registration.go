package swcoord

import "time"

// Registration binds one scope to at most three live versions:
// installing, waiting and active. Clients navigating under the scope
// are controlled by the active version.
type Registration struct {
	scope          string
	scriptURL      string
	installing     *Version
	waiting        *Version
	active         *Version
	clients        map[string]*Client
	pendingRemoval bool
	lastCheck      time.Time
	hasChecked     bool
}

func newRegistration(scope, scriptURL string) *Registration {
	return &Registration{
		scope:     scope,
		scriptURL: scriptURL,
		clients:   make(map[string]*Client),
	}
}

// comparisonVersion is the version a fetched script digest is compared
// against during an update check: active, else waiting, else installing.
func (r *Registration) comparisonVersion() *Version {
	if r.active != nil {
		return r.active
	}
	if r.waiting != nil {
		return r.waiting
	}
	return r.installing
}

// takeover moves the waiting version into the active slot, applying
// the new version's declared takeover options exactly once.
func (r *Registration) takeover() {
	next := r.waiting
	old := r.active
	if old != nil {
		if next.opts.InheritManifest {
			if next.manifest == nil {
				next.manifest = make(map[string]string)
			}
			for url, digest := range old.manifest {
				if _, declared := next.manifest[url]; !declared {
					next.manifest[url] = digest
				}
			}
		}
		old.state = StateRedundant
		if next.opts.ClaimClients {
			for _, cl := range r.clients {
				if cl.version == old {
					cl.version = next
					old.clientCount--
					next.clientCount++
				}
			}
		}
	}
	r.active = next
	next.state = StateActive
	r.waiting = nil
}

// maybePromote activates the waiting version when the active slot is
// empty, the active version controls no clients, or the waiting
// version declared skip-waiting.
func (r *Registration) maybePromote() {
	waiting := r.waiting
	if waiting == nil {
		return
	}
	if r.active == nil || r.active.clientCount == 0 || waiting.opts.SkipWaiting {
		r.takeover()
	}
}

// redundantAll forces every live version into the redundant terminal
// state; used when a pending-removal registration loses its last client.
func (r *Registration) redundantAll() {
	for _, v := range []*Version{r.installing, r.waiting, r.active} {
		if v != nil {
			v.state = StateRedundant
		}
	}
	r.installing, r.waiting, r.active = nil, nil, nil
}
