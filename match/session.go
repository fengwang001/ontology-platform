package match

func (c *Checker) NewSession(typeName string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(typeName) == 0 || len(typeName) > maxNameBytes || len(c.sessions) >= maxSessions {
		return 0, ErrInvalidArgument
	}
	if _, ok := c.types[typeName]; !ok {
		return 0, ErrInvalidArgument
	}

	id := len(c.sessions) + 1
	c.sessions[id] = &session{typeName: typeName}
	return id, nil
}
