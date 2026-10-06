package specimen

import "strconv"

func formatID(prefix string, sequence int64) string {
	return prefix + strconv.FormatInt(sequence, 10)
}

func (s *System) addActiveItemLocked(it *item) {
	list := s.activeByPatient[it.patientID]
	if list == nil {
		list = &activeList{}
		s.activeByPatient[it.patientID] = list
	}
	list.push(it)
}

func (s *System) removeActiveItemLocked(it *item) {
	list := s.activeByPatient[it.patientID]
	if list == nil {
		return
	}
	list.remove(it)
	if list.first == nil {
		delete(s.activeByPatient, it.patientID)
	}
}

func (s *System) hasActiveProjectLocked(patientID, projectID string) bool {
	list := s.activeByPatient[patientID]
	if list == nil {
		return false
	}
	for node := list.first; node != nil; node = node.next {
		if node.item.projectID == projectID {
			return true
		}
	}
	return false
}
