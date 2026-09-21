package upload

// Complete 校验并完成；etags 按分片号升序给出，长度必须等于总分片数。
// 错误优先级固定：先查缺片（ErrMissingPart），再比对 etag（ErrEtagMismatch）。
// 校验失败不会丢弃已登记的分片，可补传后再次 Complete。
func (u *Upload) Complete(etags []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.completed {
		return ErrCompleted
	}

	u.missing = u.missing[:0]
	for n := 1; n <= u.total; n++ {
		if _, ok := u.parts[n]; !ok {
			u.missing = append(u.missing, n)
		}
	}
	if len(u.missing) > 0 {
		return ErrMissingPart
	}

	if len(etags) != u.total {
		return ErrEtagMismatch
	}
	for n := 1; n <= u.total; n++ {
		if u.parts[n].etag != etags[n-1] {
			return ErrEtagMismatch
		}
	}

	u.completed = true
	return nil
}
