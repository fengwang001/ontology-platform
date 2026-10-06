package contacttracing

import "sync"

type store struct {
	mu       sync.RWMutex
	now      int64
	lastCase int64

	// patients 记录所有出现过的患者（住宿或病例）。
	patients map[string]struct{}
	// stays 按患者保存不可变住宿，追加式。
	stays map[string][]*Stay
	// openStay 记录患者当前在住的住宿指针。
	openStay map[string]*Stay
	// roomStays 按病房保存住宿指针（追加式）。
	roomStays map[string][]*Stay

	// cases 保存全部病例（含已撤销）。
	cases          map[string]*Case
	casesByPatient map[string][]*Case

	// 派生数据见 derive.go（在同一包内直接读写）。
	derived *derivedCache
}

func newStore() *store {
	return &store{
		patients:       map[string]struct{}{},
		stays:          map[string][]*Stay{},
		openStay:       map[string]*Stay{},
		roomStays:      map[string][]*Stay{},
		cases:          map[string]*Case{},
		casesByPatient: map[string][]*Case{},
		derived:        newDerivedCache(),
	}
}
