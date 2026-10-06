package alarm

import (
	"container/heap"
	"fmt"
	"io"
	"os"
	"sync"
)

type Service struct {
	mu               sync.Mutex
	config           Config
	points           map[string]*point
	conditions       map[string]map[string]struct{}
	conditionSignals map[string]bool
	expiries         *expiryHeap
	activity         *activityIndex
	lastClock        int64
	logger           io.Writer
}

func NewService(config Config, points []PointConfig) (*Service, error) {
	if config.HighManualDuration <= 0 || config.LowManualDuration <= 0 ||
		config.ChatterWindow <= 0 || config.ChatterCount <= 0 || config.ChatterDuration <= 0 {
		return nil, newError(InvalidArgument, "new service", "limits and chatter configuration must be positive")
	}
	service := &Service{
		config:           config,
		points:           make(map[string]*point),
		conditions:       make(map[string]map[string]struct{}),
		conditionSignals: make(map[string]bool),
		expiries:         &expiryHeap{},
		activity:         newActivityIndex(),
		logger:           os.Stderr,
	}
	heap.Init(service.expiries)
	seen := make(map[string]struct{}, len(points))
	for _, pointConfig := range points {
		if pointConfig.ID == "" {
			return nil, newError(InvalidArgument, "new service", "point id is empty")
		}
		if _, exists := seen[pointConfig.ID]; exists {
			return nil, newError(InvalidArgument, "new service", fmt.Sprintf("duplicate point id %q", pointConfig.ID))
		}
		seen[pointConfig.ID] = struct{}{}
		configCopy := pointConfig
		configCopy.Conditions = uniqueStrings(pointConfig.Conditions)
		runtimePoint := &point{config: configCopy}
		service.points[pointConfig.ID] = runtimePoint
		for _, condition := range configCopy.Conditions {
			if condition == "" {
				return nil, newError(InvalidArgument, "new service", "condition name is empty")
			}
			if service.conditions[condition] == nil {
				service.conditions[condition] = make(map[string]struct{})
			}
			service.conditions[condition][pointConfig.ID] = struct{}{}
		}
	}
	return service, nil
}

func (s *Service) WithLogger(writer io.Writer) *Service {
	if writer != nil {
		s.logger = writer
	}
	return s
}
