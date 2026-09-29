package fkjoin

func (j *Joiner) logf(format string, args ...any) {
	if j.logger != nil {
		j.logger.Printf(format, args...)
	}
}
