package tolling

import "time"

type Vehicle struct {
	ID           string
	InitialClass string
	ClassChanges []VehicleClassChange
}

func (v *Vehicle) ClassAt(at time.Time) string {
	class := v.InitialClass
	for _, change := range v.ClassChanges {
		if !change.EffectiveAt.After(at) {
			class = change.Class
		}
	}
	return class
}

func EdgeFee(network *Network, from, to, class string) (Money, error) {
	edge, ok := network.edge(from, to, class)
	if !ok {
		return 0, serviceError(PathUnreachable, "edge or rate is unavailable")
	}
	return edge.Rates[class], nil
}

func PathFee(network *Network, path []string, passed map[string]time.Time, vehicle *Vehicle) (Money, error) {
	if len(path) < 2 {
		return 0, serviceError(InvalidArgument, "path must contain at least two gates")
	}
	if vehicle == nil {
		return 0, serviceError(VehicleNotFound, "vehicle is nil")
	}
	var total Money
	for i := 0; i+1 < len(path); i++ {
		at, ok := passed[path[i]]
		if !ok {
			return 0, serviceError(InvalidArgument, "missing passage time for path gate")
		}
		fee, err := EdgeFee(network, path[i], path[i+1], vehicle.ClassAt(at))
		if err != nil {
			return 0, err
		}
		total += fee
	}
	return total, nil
}
