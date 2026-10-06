package waitlist

func (f *flight) pendingDue(entry *Entry, now int64) bool {
	return entry.Status == StatusPending && entry.Deadline <= now
}
