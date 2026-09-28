package vvsync

type Change struct {
	Source string
	Seq    uint64
	Key    string
	Value  string
	Stamp  string
}

type VersionVector map[string]uint64

type Replica struct{}
type Cluster struct{}

func NewCluster(limit int, names ...string) *Cluster { return nil }
func (c *Cluster) Replica(name string) (*Replica, bool) { return nil, false }

func (r *Replica) Write(key, value, stamp string) (Change, error) { return Change{}, nil }
func (r *Replica) Vector() VersionVector                          { return nil }
func (r *Replica) Log() []Change                                  { return nil }
func (r *Replica) View() map[string]Change                        { return nil }

func (r *Replica) Offer(target *Replica) (int, error) { return 0, nil }
func Sync(a, b *Replica) error                        { return nil }

func (r *Replica) OfferNaive(target *Replica) (int, error) { return 0, nil }
