package ecrepair

// repair describes one in-flight repair of a stripe.
type repair struct {
	targets []int
	finish  int
}

// stripe is the per-stripe mutable state.
type stripe struct {
	shards    []ShardState
	firstLost int // -1 when unset
	dead      bool
	inflight  *repair
}

func newStripe(n int) *stripe {
	return &stripe{shards: make([]ShardState, n), firstLost: -1}
}

// counts returns the Alive, Lost and Rebuilding shard counts.
func (st *stripe) counts() (alive, lost, rebuilding int) {
	for _, sh := range st.shards {
		switch sh {
		case Alive:
			alive++
		case Lost:
			lost++
		case Rebuilding:
			rebuilding++
		}
	}
	return alive, lost, rebuilding
}
