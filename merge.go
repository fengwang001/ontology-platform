package ontology

func validStatus(status Status) bool {
	return status == Alive || status == Suspect || status == Dead
}

func shouldReplace(current, incoming ViewEntry) (bool, string) {
	if current.Status == Dead {
		if incoming.Status == Dead && incoming.Incarnation > current.Incarnation {
			return true, "confirmed failure with a greater incarnation updates dead metadata"
		}
		return false, "confirmed failure is irreversible"
	}
	if incoming.Status == Dead {
		return true, "confirmed failure overrides every state"
	}

	switch current.Status {
	case Alive:
		switch incoming.Status {
		case Alive:
			if incoming.Incarnation > current.Incarnation {
				return true, "alive with a greater incarnation replaces alive"
			}
		case Suspect:
			if incoming.Incarnation >= current.Incarnation {
				return true, "suspect with same or greater incarnation replaces alive"
			}
		}
	case Suspect:
		switch incoming.Status {
		case Alive:
			if incoming.Incarnation > current.Incarnation {
				return true, "alive with greater incarnation refutes suspect"
			}
		case Suspect:
			if incoming.Incarnation > current.Incarnation {
				return true, "suspect with greater incarnation replaces suspect"
			}
		}
	}

	return false, "incarnation and status priority do not replace current state"
}
