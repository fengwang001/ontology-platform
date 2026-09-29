package di

// Close shuts down the scope. Resolutions starting after shutdown began
// are rejected; the call waits for in-flight resolutions to finish, then
// releases every instance the scope created in reverse creation order so
// that dependents are released before their dependencies. Repeated calls
// are idempotent.
func (s *Scope) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	if s.closing {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closing = true
	for s.active > 0 {
		s.cond.Wait()
	}
	owned := s.owned
	s.owned = nil
	s.closed = true
	s.mu.Unlock()

	releaseReverse(owned)
	close(s.done)

	c := s.container
	c.scopesMu.Lock()
	delete(c.scopes, s)
	c.scopesMu.Unlock()
	return nil
}

// Close shuts down the container. All child scopes are closed first
// (rejecting resolutions started after shutdown and waiting for in-flight
// ones); afterwards container-owned instances (singletons and root-owned
// transients) are released in reverse creation order. Repeated calls are
// idempotent and block until release work completes.
func (c *Container) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.done
		return nil
	}
	if c.closing {
		c.mu.Unlock()
		<-c.done
		return nil
	}
	c.closing = true
	for c.active > 0 {
		c.cond.Wait()
	}

	c.scopesMu.Lock()
	scopes := make([]*Scope, 0, len(c.scopes))
	for s := range c.scopes {
		scopes = append(scopes, s)
	}
	c.scopesMu.Unlock()
	c.mu.Unlock()

	// Close every scope without holding the container lock: scope
	// resolutions finishing right now only take the scope lock, and a
	// singleton single-flight leader must be able to commit.
	for _, s := range scopes {
		_ = s.Close()
	}

	c.mu.Lock()
	owned := c.owned
	c.owned = nil
	c.closed = true
	c.mu.Unlock()

	releaseReverse(owned)
	close(c.done)
	return nil
}
