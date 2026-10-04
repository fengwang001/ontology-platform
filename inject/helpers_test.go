package inject

// newJobWithValues 直接登记一个带指定快照值的作业，供打码测试使用。
func (svc *Service) newJobWithValues(values ...string) int {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.jobs == nil {
		svc.jobs = map[int]jobRecord{}
	}
	svc.next++
	svc.jobs[svc.next] = jobRecord{values: values}
	return svc.next
}
