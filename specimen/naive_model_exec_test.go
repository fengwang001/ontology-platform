package specimen

func (m *naiveModel) execute(op modelOp) (any, error) {
	if !validModelOp(op) {
		return nil, modelErr(CodeInvalidParameter)
	}
	if op.now < m.now {
		return nil, modelErr(CodeClockRollback)
	}
	switch op.name {
	case "catalog":
		m.catalog[op.project] = CatalogRequirement{
			TubeType:           op.tubeType,
			MaxDeliverySeconds: op.collectedAt,
			ColdRequired:       op.method == TransportCold,
			HemolysisTolerance: op.hemolysis,
		}
		m.now = op.now
		return nil, nil
	case "apply":
		requirements := make([]CatalogRequirement, len(op.projects))
		for index, project := range op.projects {
			requirement, exists := m.catalog[project]
			if !exists {
				return nil, modelErr(CodeNotFound)
			}
			requirements[index] = requirement
		}
		active := m.activeProjects(op.patient)
		for _, project := range op.projects {
			if active[project] {
				return nil, modelErr(CodeDuplicateRequest)
			}
		}
		m.appSeq++
		applicationID := formatID("A", m.appSeq)
		result := ApplicationResult{ApplicationID: applicationID}
		for index, project := range op.projects {
			m.itemSeq++
			id := formatID("I", m.itemSeq)
			m.items[id] = &item{
				id:               id,
				applicationID:    applicationID,
				patientID:        op.patient,
				projectID:        project,
				priority:         op.priority,
				status:           StatusWaiting,
				requirement:      requirements[index],
				enteredWaitingAt: op.now,
			}
			result.ItemIDs = append(result.ItemIDs, id)
		}
		m.now = op.now
		return result, nil
	case "collect":
		var selected []*item
		for _, id := range op.itemIDs {
			it, exists := m.items[id]
			if !exists {
				return nil, modelErr(CodeNotFound)
			}
			if it.requirement.TubeType != op.tubeType {
				return nil, modelErr(CodeTubeMismatch)
			}
			selected = append(selected, it)
		}
		for _, it := range selected {
			if it.patientID != op.patient || it.status != StatusWaiting {
				return nil, modelErr(CodeStatusMismatch)
			}
		}
		if op.collectedAt > op.now {
			return nil, modelErr(CodeInvalidTime)
		}
		for _, it := range selected {
			if op.collectedAt < it.enteredWaitingAt {
				return nil, modelErr(CodeInvalidTime)
			}
		}
		m.tubeSeq++
		tubeID := formatID("T", m.tubeSeq)
		m.tubes[tubeID] = &tube{
			id:        tubeID,
			patientID: op.patient,
			tubeType:  op.tubeType,
			active:    true,
			itemIDs:   append([]string(nil), op.itemIDs...),
		}
		for _, it := range selected {
			it.status = StatusCollected
			it.tubeID = tubeID
			it.collectedAt = op.collectedAt
		}
		m.now = op.now
		return tubeID, nil
	case "dispatch":
		t, exists := m.tubes[op.tubeType]
		if !exists {
			return nil, modelErr(CodeNotFound)
		}
		if !t.active || !modelHasSignable(m, t) {
			return nil, modelErr(CodeStatusMismatch)
		}
		t.method = op.method
		m.now = op.now
		return nil, nil
	case "sign":
		t, exists := m.tubes[op.tubeType]
		if !exists {
			return nil, modelErr(CodeNotFound)
		}
		if !t.active {
			return nil, modelErr(CodeStatusMismatch)
		}
		selected := modelSignable(m, t)
		if len(selected) == 0 {
			t.active = false
			return nil, modelErr(CodeStatusMismatch)
		}
		if t.method == "" {
			return nil, modelErr(CodeStatusMismatch)
		}
		for _, it := range selected {
			if op.now < it.collectedAt {
				return nil, modelErr(CodeInvalidTime)
			}
		}
		decisions := []SignDecision{}
		for _, it := range selected {
			reason := rejectionReason(op.now, t.method, op.hemolysis, it)
			decisions = append(decisions, SignDecision{ItemID: it.id, Accepted: reason == "", RejectionReason: reason})
			if reason == "" {
				it.status = StatusAccepted
			} else {
				it.rejectionCount++
				it.lastRejectionReason = reason
				if it.rejectionCount == 3 {
					it.status = StatusTerminated
				} else {
					it.status = StatusWaiting
					it.enteredWaitingAt = op.now
				}
			}
			it.tubeID = ""
			it.collectedAt = 0
		}
		t.active = false
		m.now = op.now
		return decisions, nil
	case "cancel":
		it, exists := m.items[op.project]
		if !exists {
			return nil, modelErr(CodeNotFound)
		}
		if it.status != StatusWaiting && it.status != StatusCollected {
			return nil, modelErr(CodeStatusMismatch)
		}
		tubeID := it.tubeID
		it.status = StatusCanceled
		it.tubeID = ""
		it.collectedAt = 0
		if t := m.tubes[tubeID]; t != nil && !modelHasSignable(m, t) {
			t.active = false
		}
		m.now = op.now
		return nil, nil
	case "query":
		views := []ItemView{}
		for _, it := range m.items {
			if it.patientID != op.patient || terminalStatus(it.status) {
				continue
			}
			view := ItemView{
				ID:                  it.id,
				ApplicationID:       it.applicationID,
				ProjectID:           it.projectID,
				Priority:            it.priority,
				Status:              it.status,
				RejectionCount:      it.rejectionCount,
				LastRejectionReason: it.lastRejectionReason,
				Collected:           it.status == StatusCollected,
			}
			if view.Collected {
				view.RemainingSeconds = it.requirement.MaxDeliverySeconds - (op.now - it.collectedAt)
			}
			views = append(views, view)
		}
		m.now = op.now
		return sortedViews(views), nil
	}
	return nil, modelErr(CodeInvalidParameter)
}

func modelSignable(m *naiveModel, t *tube) []*item {
	result := []*item{}
	for _, id := range t.itemIDs {
		it := m.items[id]
		if it.tubeID == t.id && it.status == StatusCollected {
			result = append(result, it)
		}
	}
	return result
}

func modelHasSignable(m *naiveModel, t *tube) bool {
	return len(modelSignable(m, t)) > 0
}
