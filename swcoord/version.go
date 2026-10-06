package swcoord

// VersionState is the lifecycle state of a version. Redundant is
// terminal: a redundant version rejects every operation with
// ErrStateNotAllowed and its manifest becomes unreadable.
type VersionState int

const (
	StateInstalling VersionState = iota
	StateWaiting
	StateActive
	StateRedundant
)

func (s VersionState) String() string {
	switch s {
	case StateInstalling:
		return "installing"
	case StateWaiting:
		return "waiting"
	case StateActive:
		return "active"
	case StateRedundant:
		return "redundant"
	}
	return "unknown"
}

// TakeoverOptions are declarations a version makes before it takes
// over the active slot. They are applied once, at the takeover moment.
type TakeoverOptions struct {
	// SkipWaiting lets the version take over immediately instead of
	// waiting for the active version's client count to reach zero.
	SkipWaiting bool
	// ClaimClients moves clients controlled by the old active version
	// to this version at takeover.
	ClaimClients bool
	// InheritManifest copies manifest entries declared by the old
	// active version but not by this version, as a one-time snapshot
	// taken at the takeover moment.
	InheritManifest bool
}

// Version is one script incarnation of a registration.
type Version struct {
	id          uint64
	scriptURL   string
	digest      string
	state       VersionState
	opts        TakeoverOptions
	manifest    map[string]string
	clientCount int
	reg         *Registration
}

// FetchResult answers a client's resource request. Network is true
// (and Digest empty) whenever the request must go to the network:
// uncontrolled client, redundant controlling version, or cache miss.
type FetchResult struct {
	Digest  string
	Network bool
}
