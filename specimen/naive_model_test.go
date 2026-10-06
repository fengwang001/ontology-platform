package specimen

import "sort"

type naiveModel struct {
	now     int64
	catalog map[string]CatalogRequirement
	items   map[string]*item
	tubes   map[string]*tube
	itemSeq int64
	tubeSeq int64
	appSeq  int64
}

type modelOp struct {
	name        string
	now         int64
	patient     string
	tubeType    string
	project     string
	priority    string
	method      string
	projects    []string
	itemIDs     []string
	hemolysis   int
	collectedAt int64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		catalog: map[string]CatalogRequirement{},
		items:   map[string]*item{},
		tubes:   map[string]*tube{},
	}
}

func modelErr(code string) error {
	return &Error{Code: code, Message: "naive model"}
}

func validModelOp(op modelOp) bool {
	if op.now < 0 || op.now > 1_000_000_000 {
		return false
	}
	switch op.name {
	case "catalog":
		return op.project != "" && op.tubeType != "" && op.collectedAt > 0 &&
			op.hemolysis >= 0 && op.hemolysis <= 4 &&
			(op.method == TransportCold || op.method == TransportAmbient)
	case "apply":
		if op.patient == "" || op.priority == "" || len(op.projects) < 1 || len(op.projects) > 10 {
			return false
		}
		return uniqueNonEmpty(op.projects)
	case "collect":
		if op.patient == "" || op.tubeType == "" || len(op.itemIDs) == 0 ||
			op.collectedAt < 0 || op.collectedAt > 1_000_000_000 {
			return false
		}
		return uniqueNonEmpty(op.itemIDs)
	case "dispatch":
		return op.tubeType != "" && (op.method == TransportCold || op.method == TransportAmbient)
	case "sign":
		return op.tubeType != "" && op.hemolysis >= 0 && op.hemolysis <= 4
	case "cancel":
		return op.project != ""
	case "query":
		return op.patient != ""
	default:
		return false
	}
}

func uniqueNonEmpty(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, duplicated := seen[value]; duplicated {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (m *naiveModel) activeProjects(patient string) map[string]bool {
	result := map[string]bool{}
	for _, it := range m.items {
		if it.patientID == patient && !terminalStatus(it.status) {
			result[it.projectID] = true
		}
	}
	return result
}

func terminalStatus(status string) bool {
	return status == StatusAccepted || status == StatusTerminated || status == StatusCanceled
}

func sortedViews(views []ItemView) []ItemView {
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}
