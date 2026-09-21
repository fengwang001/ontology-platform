package upload

// Complete 校验并完成上传。etags 按分片号升序给出，长度必须等于
// total。错误优先级固定：先查缺片（ErrMissingPart），再比对
// etag（ErrEtagMismatch）。校验失败不会丢弃已登记的分片。
func (u *Upload) Complete(etags []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.completed {
		return ErrCompleted
	}

	missing := make([]int, 0)
	for n := 1; n <= u.total; n++ {
		if _, ok := u.parts[n]; !ok {
			missing = append(missing, n)
		}
	}
	u.missing = missing
	if len(missing) > 0 {
		return ErrMissingPart
	}

	if len(etags) != u.total {
		return ErrEtagMismatch
	}
	for i, etag := range etags {
		if u.parts[i+1].etag != etag {
			return ErrEtagMismatch
		}
	}

	u.completed = true
	return nil
}
