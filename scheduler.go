package incremental

import (
	"strings"
	"sync"
)

type Scheduler struct {
	mu          sync.Mutex
	dispatching bool
	registry    *Registry
	graph       *DependencyGraph
	cache       *ResultCache
	checker     Checker
	logger      Logger
	stats       Stats
}

type Option func(*Scheduler)

func NewScheduler(checker Checker, options ...Option) *Scheduler {
	scheduler := &Scheduler{
		registry: NewRegistry(),
		graph:    NewDependencyGraph(),
		cache:    NewResultCache(),
		checker:  checker,
		logger:   nopLogger{},
	}
	for _, option := range options {
		option(scheduler)
	}
	return scheduler
}

func WithLogger(logger Logger) Option {
	return func(s *Scheduler) { s.logger = logger }
}

func (s *Scheduler) Apply(edit Edit) (EditOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatching {
		s.logger.Log("edit-rejected", "edit", edit)
		return EditOutcome{Rejected: true}, ErrSchedulerBusy
	}

	outcome, noOp, err := s.prepareEdit(edit)
	if err != nil {
		s.logger.Log("edit-error", "edit", edit, "error", err)
		return EditOutcome{}, err
	}
	if noOp {
		outcome.NoOp = true
		s.logger.Log("edit-noop", "edit", edit)
		return outcome, nil
	}

	s.dispatching = true
	defer func() { s.dispatching = false }()
	s.logger.Log("edit-input", "edit", edit)
	s.dispatch(edit, &outcome)
	s.logger.Log("edit-output", "edit", edit, "outcome", outcome)
	return outcome, nil
}

func (s *Scheduler) prepareEdit(edit Edit) (EditOutcome, bool, error) {
	outcome := EditOutcome{SigVersions: map[DeclID]int{}}
	if edit.ID == "" {
		return outcome, false, ErrNotFound
	}
	current, exists := s.registry.declaration(edit.ID)
	switch edit.Type {
	case AddDeclaration:
		if exists && current.Present {
			if current.SignatureSource == edit.SignatureSource &&
				current.ImplementationSource == edit.ImplementationSource {
				return outcome, true, nil
			}
			return outcome, false, ErrNotFound
		}
		if !exists {
			s.registry.put(edit.ID, Declaration{
				ID:                   edit.ID,
				Present:              true,
				SignatureSource:      edit.SignatureSource,
				ImplementationSource: edit.ImplementationSource,
			})
		} else {
			current.Present = true
			current.SignatureSource = edit.SignatureSource
			current.ImplementationSource = edit.ImplementationSource
			s.registry.setDeclaration(edit.ID, current)
		}
	case SetSignature:
		if !exists || !current.Present {
			return outcome, false, ErrNotFound
		}
		if current.SignatureSource == edit.SignatureSource {
			return outcome, true, nil
		}
		current.SignatureSource = edit.SignatureSource
		s.registry.setDeclaration(edit.ID, current)
	case SetImplementation:
		if !exists || !current.Present {
			return outcome, false, ErrNotFound
		}
		if current.ImplementationSource == edit.ImplementationSource {
			return outcome, true, nil
		}
		current.ImplementationSource = edit.ImplementationSource
		s.registry.setDeclaration(edit.ID, current)
	case DeleteDeclaration:
		if !exists || !current.Present {
			return outcome, true, nil
		}
		s.registry.deleteDeclaration(edit.ID)
	default:
		return outcome, false, ErrNotFound
	}
	return outcome, false, nil
}

func (s *Scheduler) dispatch(edit Edit, outcome *EditOutcome) {
	if edit.Type == SetImplementation {
		s.invalidateImplementation(edit.ID)
		s.runImplementation(edit.ID, outcome)
		s.finish(outcome)
		return
	}

	signatureQueue := map[DeclID]bool{}
	implementationTargets := map[DeclID]bool{}

	if edit.Type == DeleteDeclaration {
		rootChanged := s.commitMissingSignature(edit.ID, outcome)
		if rootChanged {
			for _, id := range s.graph.signatureDependents(edit.ID) {
				signatureQueue[id] = true
			}
			for _, id := range s.graph.implementationDependents(edit.ID) {
				implementationTargets[id] = true
			}
		}
	} else {
		signatureQueue[edit.ID] = true
		implementationTargets[edit.ID] = true
		for _, id := range s.graph.signatureDependents(edit.ID) {
			signatureQueue[id] = true
		}
	}
	implementationDependents := map[DeclID]bool{}

	checked := map[DeclID]bool{}
	if edit.Type == DeleteDeclaration {
		checked[edit.ID] = true
	}
	for _, id := range s.graph.signatureDependents(edit.ID) {
		signatureQueue[id] = true
	}
	{
		potential := s.graph.signatureClosure(sortedBoolKeys(signatureQueue))
		potentialSet := map[DeclID]bool{}
		for _, id := range potential {
			potentialSet[id] = true
		}
		potentialSet[edit.ID] = true
		orderedQueue := s.graph.topologicalOrder(sortedBoolKeys(signatureQueue))
		for firstUncheckedIn(orderedQueue, checked) != "" {
			id := firstUncheckedIn(orderedQueue, checked)
			if id == "" {
				break
			}
			group := s.graph.exactComponent(id, potentialSet)
			changed := s.runSignatureGroup(group.members, outcome, implementationTargets, checked)
			for _, changedID := range changed {
				for _, dependent := range s.graph.signatureDependents(changedID) {
					if !checked[dependent] {
						signatureQueue[dependent] = true
					}
					implementationDependents[dependent] = true
				}
				for _, dependent := range s.graph.implementationDependents(changedID) {
					implementationDependents[dependent] = true
				}
			}
			orderedQueue = s.graph.topologicalOrder(sortedBoolKeys(signatureQueue))
		}
	}

	for id := range implementationDependents {
		implementationTargets[id] = true
	}
	for _, id := range sortedBoolKeys(implementationTargets) {
		if decl, ok := s.registry.declaration(id); ok && decl.Present {
			s.runImplementation(id, outcome)
		}
	}
	s.finish(outcome)
}

func (s *Scheduler) commitMissingSignature(id DeclID, outcome *EditOutcome) bool {
	s.cache.invalidateImplementation(id)
	s.cache.invalidateSignature(id)
	current := s.registry.currentSignature(id)
	missing := SignatureResult{
		Present:   false,
		Signature: MissingSignature(id),
		Basis:     Basis{},
	}
	changed := s.registry.commitSignature(id, missing)
	if changed {
		outcome.SigVersions[id] = current.Version + 1
	}
	s.cache.putSignature(id, s.registry.currentSignature(id))
	s.graph.replaceSignatureDependencies(id, nil)
	return changed
}

func (s *Scheduler) runSignatureGroup(ids []DeclID, outcome *EditOutcome, implementationTargets map[DeclID]bool, checked map[DeclID]bool) []DeclID {
	members := append([]DeclID(nil), ids...)
	oldResults := map[DeclID]SignatureResult{}
	trials := map[DeclID]SignatureResult{}
	allReads := map[DeclID]map[DeclID]bool{}
	groupFailure := false

	for {
		allReads = map[DeclID]map[DeclID]bool{}
		for _, id := range members {
			if _, ok := oldResults[id]; !ok {
				oldResults[id] = s.registry.currentSignature(id)
			}
			delete(trials, id)
		}

		cycleFailure := false
		for _, id := range members {
			decl, ok := s.registry.declaration(id)
			if !ok || !decl.Present {
				allReads[id] = map[DeclID]bool{}
				continue
			}
			reads := map[DeclID]bool{}
			context := &CheckContext{id: id, reads: reads, lookup: s.lookupForTrial(trials)}
			signature := s.checker.CheckSignature(context, decl)
			trials[id] = SignatureResult{Present: true, Signature: signature}
			allReads[id] = reads
		}

		component := s.graph.trialCycleComponent(members, onlyTrialEdges(members, allReads)).members
		cycleFailure = len(component) > 1
		for _, id := range component {
			if allReads[id] != nil && allReads[id][id] {
				cycleFailure = true
			}
		}
		if len(component) == 0 {
			component = ids
		}
		members = component
		groupFailure = cycleFailure
		if groupFailure {
			errorSignature := groupErrorSignature(members)
			for _, id := range members {
				trials[id] = SignatureResult{Present: true, Signature: errorSignature}
			}
		}
		break
	}

	unionReads := map[DeclID]bool{}
	if groupFailure {
		for _, dependencies := range allReads {
			for dep := range dependencies {
				unionReads[dep] = true
			}
		}
	}

	changedIDs := []DeclID{}
	finalVersions := map[DeclID]int{}
	changedSet := map[DeclID]bool{}
	for _, id := range members {
		old := oldResults[id]
		changed := !old.Present || !trials[id].Signature.Equal(old.Signature)
		if changed {
			changedSet[id] = true
			finalVersions[id] = old.Version + 1
		} else {
			finalVersions[id] = old.Version
		}
	}
	for _, id := range members {
		trial := trials[id]
		basisReads := map[DeclID]bool{}
		if groupFailure {
			for _, member := range members {
				basisReads[member] = true
			}
			for dep := range unionReads {
				basisReads[dep] = true
			}
		} else {
			for dep := range allReads[id] {
				basisReads[dep] = true
			}
		}
		trial.Basis = makeBasisWithVersions(s.lookupForTrial(trials), basisReads, finalVersions)
		changed := finalVersions[id] != oldResults[id].Version
		s.registry.commitSignatureForce(id, trial, changed)
		if changed {
			changedIDs = append(changedIDs, id)
			outcome.SigVersions[id] = s.registry.currentSignature(id).Version
		}
		s.cache.putSignature(id, s.registry.currentSignature(id))
		if changed || groupFailure {
			s.graph.replaceSignatureDependencies(id, allReads[id])
		}
		s.logger.Log("signature-basis", "id", id, "signature", s.registry.currentSignature(id).Signature, "basis", trial.Basis)
		outcome.Rechecked = appendUnique(outcome.Rechecked, id)
		checked[id] = true
	}

	if len(changedIDs) == 0 {
		outcome.EarlyStopped = append(outcome.EarlyStopped, members...)
		return nil
	}

	for _, id := range changedIDs {
		implementationTargets[id] = true
		for _, dependent := range s.graph.signatureDependents(id) {
			implementationTargets[dependent] = true
		}
	}
	return changedIDs
}

func sameIDSet(a, b []DeclID) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[DeclID]bool{}
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if !set[id] {
			return false
		}
	}
	return true
}

func (s *Scheduler) lookupForTrial(trials map[DeclID]SignatureResult) func(DeclID) (SignatureResult, bool) {
	return func(id DeclID) (SignatureResult, bool) {
		if result, ok := trials[id]; ok {
			return result, true
		}
		return s.registry.snapshotSignature(id)
	}
}

func makeBasis(lookup func(DeclID) (SignatureResult, bool), ids map[DeclID]bool) Basis {
	basis := Basis{}
	for id, include := range ids {
		if !include {
			continue
		}
		result, _ := lookup(id)
		basis[id] = BasisEntry{
			Version:   result.Version,
			Present:   result.Present,
			Signature: result.Signature,
		}
	}
	return basis
}

func makeBasisWithVersions(lookup func(DeclID) (SignatureResult, bool), ids map[DeclID]bool, versions map[DeclID]int) Basis {
	basis := makeBasis(lookup, ids)
	for id := range basis {
		if version, ok := versions[id]; ok {
			entry := basis[id]
			entry.Version = version
			basis[id] = entry
		}
	}
	return basis
}

func groupErrorSignature(ids []DeclID) Signature {
	return Signature{
		Value:     "",
		ErrorText: "cyclic signature group error: " + strings.Join(declStrings(ids), ","),
	}
}

func declStrings(ids []DeclID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = string(id)
	}
	return result
}

func (s *Scheduler) invalidateImplementation(id DeclID) {
	s.cache.invalidateImplementation(id)
}

func (s *Scheduler) runImplementation(id DeclID, outcome *EditOutcome) {
	decl, ok := s.registry.declaration(id)
	if !ok || !decl.Present {
		return
	}
	reads := map[DeclID]bool{}
	context := &CheckContext{
		id:     id,
		reads:  reads,
		lookup: s.registry.snapshotSignature,
	}
	signature := s.checker.CheckImplementation(context, decl)
	s.graph.replaceImplementationDependencies(id, reads)
	result := ImplementationResult{
		Present:   true,
		Signature: signature,
		Basis:     makeBasis(s.registry.snapshotSignature, reads),
	}
	s.cache.putImplementation(id, result)
	s.logger.Log("implementation-basis", "id", id, "result", signature, "basis", result.Basis)
	outcome.Rechecked = appendUnique(outcome.Rechecked, id)
}

func firstUnchecked(set map[DeclID]bool, checked map[DeclID]bool) DeclID {
	return firstUncheckedIn(sortedBoolKeys(set), checked)
}

func firstUncheckedIn(ids []DeclID, checked map[DeclID]bool) DeclID {
	for _, id := range ids {
		if !checked[id] {
			return id
		}
	}
	return ""
}

func appendUnique(ids []DeclID, id DeclID) []DeclID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func onlyTrialEdges(members []DeclID, reads map[DeclID]map[DeclID]bool) map[DeclID]map[DeclID]bool {
	trial := map[DeclID]map[DeclID]bool{}
	for _, id := range members {
		trial[id] = reads[id]
	}
	return trial
}

func dedupeIDs(ids []DeclID) []DeclID {
	result := []DeclID{}
	for _, id := range ids {
		result = appendUnique(result, id)
	}
	return result
}

func (s *Scheduler) finish(outcome *EditOutcome) {
	sortIDs := func(ids []DeclID) {
		for i := 1; i < len(ids); i++ {
			for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
				ids[j-1], ids[j] = ids[j], ids[j-1]
			}
		}
	}
	sortIDs(outcome.Rechecked)
	sortIDs(outcome.EarlyStopped)
	outcome.Rechecked = dedupeIDs(outcome.Rechecked)
	outcome.EarlyStopped = dedupeIDs(outcome.EarlyStopped)
	outcome.Stats.LastRecheckedDeclarations = len(outcome.Rechecked)
	outcome.Stats.LastEarlyStoppedDeclarations = len(outcome.EarlyStopped)
	outcome.Stats.ReuseCount = s.stats.ReuseCount
	s.stats.LastRecheckedDeclarations = outcome.Stats.LastRecheckedDeclarations
	s.stats.LastEarlyStoppedDeclarations = outcome.Stats.LastEarlyStoppedDeclarations
}

func (s *Scheduler) SignatureResult(id DeclID) (SignatureResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.cache.signature(id)
	if ok && !s.registry.basisCurrent(result.Basis) {
		return SignatureResult{}, false
	}
	if ok {
		s.stats.ReuseCount++
	}
	return result, ok
}

func (s *Scheduler) ImplementationResult(id DeclID) (ImplementationResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.cache.implementation(id)
	if ok && !s.registry.basisCurrent(result.Basis) {
		return ImplementationResult{}, false
	}
	if ok {
		s.stats.ReuseCount++
	}
	return result, ok
}

func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}
