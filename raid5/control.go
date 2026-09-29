package raid5

// FailDisk 标记一块盘失效（模拟拔盘）。
// 拒绝场景（不触碰任何盘）：盘号越界；已有一块盘失效。
func (v *Volume) FailDisk(index int) error {
	if index < 0 || index >= v.n {
		return ErrDiskIndexOutOfRange
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failedDisk >= 0 {
		return ErrTwoDisksFailed
	}
	if v.disks[index].Failed() {
		return ErrTwoDisksFailed
	}
	v.disks[index].Fail()
	v.failedDisk = index
	return nil
}

// ReplaceDisk 用新盘替换失效盘并（后台）启动重建。
// 拒绝场景（不触碰任何盘）：盘号越界；目标盘当前未失效；重建已在进行。
func (v *Volume) ReplaceDisk(index int, fresh Disk) error {
	if index < 0 || index >= v.n {
		return ErrDiskIndexOutOfRange
	}
	if fresh == nil || fresh.Index() != index {
		return ErrFormat
	}
	v.mu.Lock()
	if v.rebuildDisk >= 0 {
		v.mu.Unlock()
		return ErrRebuildInProgress
	}
	if !v.disks[index].Failed() {
		v.mu.Unlock()
		return ErrRebuildHealthyDisk
	}
	v.disks[index] = fresh
	v.failedDisk = -1
	v.rebuildDisk = index
	v.mu.Unlock()
	v.startRebuild(index)
	return nil
}

// startRebuild 对目标盘执行/续建全部条带，结束后清除重建状态。
func (v *Volume) startRebuild(target int) {
	if err := v.disks[target].MarkRebuilding(); err != nil {
		return
	}
	v.rebuildWG.Add(1)
	go func() {
		defer v.rebuildWG.Done()
		for s := 0; s < v.stripes; s++ {
			if v.rebuildGate != nil {
				if ok := <-v.rebuildGate; !ok {
					return
				}
			}
			v.stripeLocks[s].Lock()
			rebuilt := v.disks[target].Rebuilt(s)
			if !rebuilt {
				buf, err := v.xorOtherDisks(s, target)
				if err == nil {
					err = v.disks[target].WriteBlockForRebuild(s, buf)
				}
				v.stripeLocks[s].Unlock()
				if err != nil {
					// 重建期间发生二次失效：停止重建，卷进入双失效不可用。
					v.mu.Lock()
					if v.rebuildDisk == target {
						v.failedDisk = target
						v.rebuildDisk = -1
					}
					v.mu.Unlock()
					return
				}
			} else {
				v.stripeLocks[s].Unlock()
			}
		}
		v.mu.Lock()
		_ = v.disks[target].FinishRebuild()
		if v.rebuildDisk == target {
			v.rebuildDisk = -1
		}
		v.mu.Unlock()
	}()
}

// RebuildDone 等待后台重建结束；不在重建时立即返回。
func (v *Volume) RebuildDone() {
	v.rebuildWG.Wait()
}

// SetCrashHook 安装一次性断电注入钩子。
func (v *Volume) SetCrashHook(h *CrashHook) {
	v.hookMu.Lock()
	v.hook = h
	v.hookMu.Unlock()
}

// Close 等待重建结束并关闭日志与成员盘。
func (v *Volume) Close() error {
	v.rebuildWG.Wait()
	var firstErr error
	if v.journal != nil {
		if err := v.journal.Close(); err != nil {
			firstErr = err
		}
	}
	for _, d := range v.disks {
		if err := d.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
