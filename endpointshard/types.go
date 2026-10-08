package endpointshard

// Endpoint is a single service endpoint. IDs are unique within a
// service. Region and the two status bits (Healthy, Terminating) are
// updated in place by Sync; they never cause a shard move.
type Endpoint struct {
	ID          string
	Region      string
	Healthy     bool
	Terminating bool
}

// Ready reports the derived condition: healthy and not terminating.
func (e Endpoint) Ready() bool { return e.Healthy && !e.Terminating }

// Servable reports the derived condition: healthy.
func (e Endpoint) Servable() bool { return e.Healthy }

// IsTerminating reports the derived condition: the terminating bit.
func (e Endpoint) IsTerminating() bool { return e.Terminating }

// ShardChange describes one shard touched by a Sync or Resize call.
// Deleted shards are reported with Deleted=true and no endpoints.
// Generation is the shard's modification generation after the call;
// it is incremented exactly when the shard appears in a report
// (including creation).
type ShardChange struct {
	ShardNum   int
	Generation uint64
	Deleted    bool
	Endpoints  []Endpoint
}

// SyncReport is the change report of one Sync or Resize call: exactly
// the shards whose content changed, that were created, or that were
// deleted. Sorted by shard number. An empty report means no state
// changed and no generation was bumped.
type SyncReport struct {
	Changes []ShardChange
}

// Changed returns the shard numbers present in the report.
func (r *SyncReport) Changed() []int {
	nums := make([]int, 0, len(r.Changes))
	for _, c := range r.Changes {
		nums = append(nums, c.ShardNum)
	}
	return nums
}

// QueryResult is the answer to a consumer Query. Fallback is true
// when no ready endpoint existed and the result was derived from the
// fallback rule (servable and terminating endpoints), even if the
// fallback set itself was empty.
type QueryResult struct {
	Endpoints []Endpoint
	Fallback  bool
}

// ShardInfo is a read-only view of one live shard, used by Inspect.
type ShardInfo struct {
	Num        int
	Generation uint64
	Endpoints  []Endpoint
}
