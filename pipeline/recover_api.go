package pipeline

// ensureFinalized completes merge/finalize for a pipeline opened in
// recovery mode at the spill/merge boundary (idempotent).
func (p *Pipeline) ensureFinalized() error {
	if _, err := p.finalizedOnDisk(); err != nil {
		return err
	}
	return nil
}

func (p *Pipeline) finalizedOnDisk() (bool, error) {
	cp, found, err := loadCheckpoint(p.dir)
	if err != nil || !found {
		return false, err
	}
	if cp.Phase == PhaseFinalize {
		return true, nil
	}
	if err := p.recover(cp); err != nil {
		return false, err
	}
	return true, nil
}

func (p *Pipeline) maybeCrash(phase string) error {
	if p.faults == nil || p.faults.CrashAfter == nil {
		return nil
	}
	if p.faults.CrashAfter(phase) {
		return errCrashInjected
	}
	return nil
}
