package swcoord

// Client is a page identified by a stable ID. Its controlling version
// is fixed for the whole page lifetime: it changes only on navigation
// or when a takeover declares claim-clients.
type Client struct {
	id      string
	reg     *Registration
	version *Version
}

// release detaches the client from its controlling version and
// registration, firing the count-zero takeover or the pending-removal
// finalization that the release may unblock.
func (c *Coordinator) releaseClient(cl *Client) {
	if cl.version != nil {
		cl.version.clientCount--
		cl.version = nil
	}
	reg := cl.reg
	if reg == nil {
		return
	}
	delete(reg.clients, cl.id)
	cl.reg = nil
	if reg.pendingRemoval {
		if len(reg.clients) == 0 {
			c.finalizeRemoval(reg)
		}
		return
	}
	reg.maybePromote()
}
