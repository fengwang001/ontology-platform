package pumpstation

func (p *pump) totalRuntime(now int64) int64 {
	total := p.cumulativeTime
	if p.running {
		total += now - p.startedAt
	}
	return total
}

func (p *pump) meetsMinimumRun(now int64, duration int64) bool {
	return p.running && now-p.startedAt >= duration
}

func (p *pump) meetsMinimumStop(now int64, duration int64) bool {
	return !p.running && now-p.stoppedAt >= duration
}

func (p *pump) start(now int64) {
	p.running = true
	p.startedAt = now
	p.lastStartedAt = now
}

func (p *pump) stop(now int64) {
	p.cumulativeTime = p.totalRuntime(now)
	p.running = false
	p.stoppedAt = now
}
