package sw

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

type Version struct {
	id              uint64
	state           VersionState
	digest          string
	manifest        map[string]string
	clients         map[string]struct{}
	skipWaiting     bool
	claimClients    bool
	inheritManifest bool
	reg             *Registration
}

func (v *Version) ID() uint64          { return v.id }
func (v *Version) State() VersionState { return v.state }
func (v *Version) Digest() string      { return v.digest }
func (v *Version) ClientCount() int    { return len(v.clients) }

func (v *Version) ManifestEntry(url string) (string, bool) {
	d, ok := v.manifest[url]
	return d, ok
}

func (v *Version) Manifest() map[string]string {
	out := make(map[string]string, len(v.manifest))
	for k, d := range v.manifest {
		out[k] = d
	}
	return out
}

func (v *Version) Clients() []string {
	out := make([]string, 0, len(v.clients))
	for id := range v.clients {
		out = append(out, id)
	}
	return out
}
