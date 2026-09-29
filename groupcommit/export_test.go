package groupcommit

// setBatchGate installs a test-only gate that the worker awaits once, after
// the first request is queued and before any batch is cut. Closing gate after
// preloading makes batch composition deterministic.
func (gc *GroupCommitter) setBatchGate(gate <-chan struct{}) {
	gc.batchGate = gate
}
