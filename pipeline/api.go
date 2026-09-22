package pipeline

import "path/filepath"

// MaxResident reports the historical peak of accounted resident bytes.
func (p *Pipeline) MaxResident() int64 { return p.b.Peak() }

// Limit returns the configured resident-byte hard limit.
func (p *Pipeline) Limit() int64 { return p.b.Limit() }

// Ingested reports how many records were durably ingested.
func (p *Pipeline) Ingested() int64 {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.ingested
}

// RunCount reports the number of spill run files.
func (p *Pipeline) RunCount() int {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return len(p.runs)
}

// Comparisons reports the key-comparison count of the last merge.
func (p *Pipeline) Comparisons() int64 { return p.mergeCmp }

// OutputPath returns the final sorted output file path.
func (p *Pipeline) OutputPath() string { return filepath.Join(p.dir, "output.osrt") }
