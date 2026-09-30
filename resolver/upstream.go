package resolver

// queryUpstream 查询单个名字：同一名字上的并发调用合并为一次上游请求，
// 等待者得到同一份应答或同一个错误。
func (c *Cache) queryUpstream(name Name) (Answer, error) {
	c.mu.Lock()
	if call := c.upCalls[name]; call != nil {
		c.mu.Unlock()
		c.logf("  hop %q upstream call already in-flight, sharing it", name)
		<-call.done
		return call.ans, call.err
	}
	call := &inflightUpstream{done: make(chan struct{})}
	c.upCalls[name] = call
	c.mu.Unlock()

	c.logf("  hop %q issuing upstream query", name)
	call.ans, call.err = c.upstream(name)

	c.mu.Lock()
	delete(c.upCalls, name)
	c.mu.Unlock()
	close(call.done)

	if call.err != nil {
		c.logf("  hop %q upstream answered error=%v", name, call.err)
	} else {
		c.logf("  hop %q upstream answered kind=%s", name, newEntry(call.ans, 0).kind)
	}
	return call.ans, call.err
}
