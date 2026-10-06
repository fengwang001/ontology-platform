package compaction

import (
	"math/big"
	"sort"
	"sync"
)

type Service struct {
	mu         sync.RWMutex
	config     Config
	logger     OperationLog
	files      map[uint64]*File
	layers     []layerState
	plans      map[uint64]*storedPlan
	occupied   map[uint64]uint64
	nextPlanID uint64
}

type layerState struct {
	files      []*File
	tree       *intervalTree
	totalBytes *big.Int
	lastEnd    []byte
}

type storedPlan struct {
	plan           Plan
	lastEndUpdated bool
}

type noopLog struct{}

func (noopLog) Log(string, any, any, string) {}

func NewService(config Config, logger OperationLog) (*Service, error) {
	if err := validateConfig(config); err != nil {
		return nil, ErrInvalidArgument
	}
	if logger == nil {
		logger = noopLog{}
	}
	service := &Service{
		config:     config,
		logger:     logger,
		files:      make(map[uint64]*File),
		layers:     make([]layerState, config.Layers),
		plans:      make(map[uint64]*storedPlan),
		occupied:   make(map[uint64]uint64),
		nextPlanID: 1,
	}
	for index := range service.layers {
		layer := &service.layers[index]
		layer.tree = &intervalTree{}
		layer.totalBytes = new(big.Int)
	}
	return service, nil
}

func (s *Service) RegisterFile(file File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateFile(file, s.config); err != nil {
		s.logger.Log("RegisterFile", file, errorOutput(err), err.Error())
		return err
	}
	if _, exists := s.files[file.ID]; exists {
		s.logger.Log("RegisterFile", file, errorOutput(ErrInvalidArgument), "duplicate file id")
		return ErrInvalidArgument
	}
	layer := &s.layers[file.Layer]
	registered := cloneFile(file)
	if file.Layer > 0 {
		for _, existing := range layer.tree.overlaps(registered.MinKey, registered.MaxKey) {
			if intervalsOverlapStrict(registered.MinKey, registered.MaxKey, existing.MinKey, existing.MaxKey) {
				s.logger.Log("RegisterFile", file, errorOutput(ErrLayerInvariant), "non-zero layer overlap")
				return ErrLayerInvariant
			}
		}
		layer.tree.insert(registered)
		layer.files = append(layer.files, registered)
		sort.Slice(layer.files, func(i, j int) bool { return layerFileOrder(layer.files[i], layer.files[j]) < 0 })
	} else {
		layer.files = append(layer.files, registered)
		sort.Slice(layer.files, func(i, j int) bool { return layer.files[i].ID < layer.files[j].ID })
	}
	s.files[registered.ID] = registered
	layer.totalBytes.Add(layer.totalBytes, new(big.Int).SetUint64(registered.Bytes))
	s.logger.Log("RegisterFile", file, registered, "file registered and layer state updated")
	return nil
}

func validateFile(file File, config Config) error {
	if file.Layer < 0 || file.Layer >= config.Layers || file.MinKey == nil || file.MaxKey == nil || compareKeys(file.MinKey, file.MaxKey) > 0 {
		return ErrInvalidArgument
	}
	return nil
}

func cloneFile(file File) *File {
	return &File{
		ID:     file.ID,
		Layer:  file.Layer,
		MinKey: append([]byte(nil), file.MinKey...),
		MaxKey: append([]byte(nil), file.MaxKey...),
		Bytes:  file.Bytes,
	}
}

func intervalsOverlap(minLeft, maxLeft, minRight, maxRight []byte) bool {
	return compareKeys(minLeft, maxRight) <= 0 && compareKeys(minRight, maxLeft) <= 0
}

func intervalsOverlapStrict(minLeft, maxLeft, minRight, maxRight []byte) bool {
	return compareKeys(minLeft, maxRight) < 0 && compareKeys(minRight, maxLeft) < 0
}

func errorOutput(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
