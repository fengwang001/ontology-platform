package routeexecution

type simulationSeed struct {
	fromID         string
	departureAt    uint64
	drivingSeconds uint64
	hasAnchor      bool
	index          int
}

type plannedStop struct {
	stop     Stop
	canceled bool
	reported bool
	arrival  uint64
}

type nodeResult struct {
	result            StopResult
	seed              simulationSeed
	driving           uint64
	nextSeed          simulationSeed
	physicalSeed      simulationSeed
	prevPhysicalIndex int
}

type simulationInstrument struct {
	visits int
}

func simulateSuffix(
	stops []plannedStop,
	travel map[string]map[string]uint64,
	config Config,
	seed simulationSeed,
	startIndex int,
) []nodeResult {
	return simulateSuffixInstrumented(stops, travel, config, seed, startIndex, nil)
}

func simulateSuffixInstrumented(
	stops []plannedStop,
	travel map[string]map[string]uint64,
	config Config,
	seed simulationSeed,
	startIndex int,
	instrument *simulationInstrument,
) []nodeResult {
	results := make([]nodeResult, len(stops))
	anchor := seed
	physicalAnchor := seed
	pendingSkipped := make([]int, 0)
	rest := uint64(0)
	physicalIndex := seed.index

	for index := startIndex; index < len(stops); index++ {
		if instrument != nil {
			instrument.visits++
		}
		planned := stops[index]
		if planned.canceled {
			results[index] = canceledNode(index, planned, anchor)
			results[index].prevPhysicalIndex = physicalIndex
			continue
		}

		travelSeconds := directTravelSeconds(planned, travel, config, physicalAnchor)
		rest = 0
		if !planned.reported && needsRest(currentDriving(physicalAnchor, config), travelSeconds, config) {
			rest = config.RestSeconds
		}
		arrival, preServiceDriving := plannedArrivalWithRest(planned, travel, config, physicalAnchor, rest)

		if !planned.reported && isHardLate(planned, arrival) {
			pendingSkipped = append(pendingSkipped, index)
			node := skippedNode(index, planned, arrival, preServiceDriving, physicalAnchor)
			node.prevPhysicalIndex = physicalIndex
			results[index] = node
			physicalAnchor = simulationSeed{
				fromID:         planned.stop.ID,
				departureAt:    arrival,
				drivingSeconds: preServiceDriving,
				hasAnchor:      true,
				index:          index,
			}
			physicalIndex = index
			continue
		}

		for _, skippedIndex := range pendingSkipped {
			results[skippedIndex].seed = anchor
		}
		pendingSkipped = pendingSkipped[:0]

		node := servedNode(index, planned, arrival, preServiceDriving, anchor, config)
		node.prevPhysicalIndex = physicalIndex
		results[index] = node
		node.nextSeed.index = index
		node.physicalSeed.index = index
		results[index] = node
		anchor = node.nextSeed
		physicalAnchor = node.nextSeed
		physicalIndex = index
	}
	return results
}

func canceledNode(index int, planned plannedStop, anchor simulationSeed) nodeResult {
	return nodeResult{
		result: StopResult{
			Index:  index,
			ID:     planned.stop.ID,
			Status: StatusCanceled,
			Active: false,
		},
		seed: anchor,
	}
}

func skippedNode(
	index int,
	planned plannedStop,
	arrival uint64,
	driving uint64,
	serviceAnchor simulationSeed,
) nodeResult {
	return nodeResult{
		result: StopResult{
			Index:          index,
			ID:             planned.stop.ID,
			Status:         StatusSkipped,
			ArrivalSeconds: arrival,
			Active:         true,
			Reported:       planned.reported,
			HasArrival:     true,
		},
		seed:    serviceAnchor,
		driving: driving,
		physicalSeed: simulationSeed{
			fromID:         planned.stop.ID,
			departureAt:    arrival,
			drivingSeconds: driving,
			hasAnchor:      true,
		},
	}
}

func servedNode(
	index int,
	planned plannedStop,
	arrival uint64,
	preServiceDriving uint64,
	serviceAnchor simulationSeed,
	config Config,
) nodeResult {
	serviceStart := arrival
	status := StatusOnTime
	if arrival < planned.stop.Window.LeftSeconds {
		serviceStart = planned.stop.Window.LeftSeconds
		status = StatusWaited
	} else if arrival > planned.stop.Window.RightSeconds {
		status = StatusLate
	}

	wait := uint64(0)
	if serviceStart > arrival {
		wait = serviceStart - arrival
	}
	departure := serviceStart + planned.stop.ServiceSeconds
	drivingAfterDwell := preServiceDriving
	if wait+planned.stop.ServiceSeconds >= config.RestSeconds {
		drivingAfterDwell = 0
	}

	return nodeResult{
		result: StopResult{
			Index:               index,
			ID:                  planned.stop.ID,
			Status:              status,
			ArrivalSeconds:      arrival,
			ServiceStartSeconds: serviceStart,
			DepartureSeconds:    departure,
			Active:              true,
			Reported:            planned.reported,
			HasArrival:          true,
			HasService:          true,
			HasDeparture:        true,
		},
		seed:    serviceAnchor,
		driving: drivingAfterDwell,
		physicalSeed: simulationSeed{
			fromID:         planned.stop.ID,
			departureAt:    departure,
			drivingSeconds: drivingAfterDwell,
			hasAnchor:      true,
		},
		nextSeed: simulationSeed{
			fromID:         planned.stop.ID,
			departureAt:    departure,
			drivingSeconds: drivingAfterDwell,
			hasAnchor:      true,
		},
	}
}

func plannedArrivalWithRest(
	planned plannedStop,
	travel map[string]map[string]uint64,
	config Config,
	anchor simulationSeed,
	rest uint64,
) (uint64, uint64) {
	if planned.reported {
		if anchor.hasAnchor {
			elapsed := uint64(0)
			if planned.arrival > anchor.departureAt {
				elapsed = planned.arrival - anchor.departureAt
			}
			return planned.arrival, elapsed
		}
		return planned.arrival, config.InitialContinuousSeconds
	}

	if !anchor.hasAnchor {
		driving := config.InitialContinuousSeconds
		travelSeconds := travel[config.OriginID][planned.stop.ID]
		if rest > 0 {
			driving = 0
		}
		arrival := config.DepartureSeconds + rest + travelSeconds
		return arrival, driving + travelSeconds
	}

	travelSeconds := travel[anchor.fromID][planned.stop.ID]
	driving := anchor.drivingSeconds
	if rest > 0 {
		driving = 0
	}
	return anchor.departureAt + rest + travelSeconds, driving + travelSeconds
}

func directTravelSeconds(
	planned plannedStop,
	travel map[string]map[string]uint64,
	config Config,
	anchor simulationSeed,
) uint64 {
	if planned.reported && anchor.hasAnchor {
		if planned.arrival > anchor.departureAt {
			return planned.arrival - anchor.departureAt
		}
		return 0
	}

	from := config.OriginID
	if anchor.hasAnchor {
		from = anchor.fromID
	}
	return travel[from][planned.stop.ID]
}

func needsRest(currentDriving uint64, nextDriving uint64, config Config) bool {
	return currentDriving+nextDriving > config.MaxContinuousDrivingSeconds
}

func currentDriving(anchor simulationSeed, config Config) uint64 {
	if anchor.hasAnchor {
		return anchor.drivingSeconds
	}
	return config.InitialContinuousSeconds
}

func isHardLate(planned plannedStop, arrival uint64) bool {
	return planned.stop.Type == HardWindow && arrival > planned.stop.Window.RightSeconds
}
