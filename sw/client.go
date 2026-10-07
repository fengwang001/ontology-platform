package sw

type Client struct {
	id           string
	controlledBy *Version
}

func (c *Client) ID() string { return c.id }

func (c *Client) ControlledBy() (uint64, bool) {
	if c.controlledBy == nil {
		return 0, false
	}
	return c.controlledBy.id, true
}
