package ontology

import (
	"crypto/rand"
	"fmt"
)

// DefaultPageSize 是未显式指定页大小时的每页对象数。
const DefaultPageSize = 100

// TruncationReason 是三态截断标记。
type TruncationReason int

const (
	// TruncationNone 完整：深度与扇出限制下应纳入的对象已全部返回。
	TruncationNone TruncationReason = iota
	// TruncationFanout 因扇出截断（优先级高于深度截断）。
	TruncationFanout
	// TruncationDepth 因深度截断。
	TruncationDepth
)

// String 返回截断原因的可读名称。
func (r TruncationReason) String() string {
	switch r {
	case TruncationFanout:
		return "fanout"
	case TruncationDepth:
		return "depth"
	default:
		return "complete"
	}
}

// TraverseParams 是单次遍历请求的参数。
type TraverseParams struct {
	Start     ObjectID
	MaxDepth  int
	MaxFanout int
	Token     string
	PageSize  int
}

// Page 是一页遍历结果。
type Page struct {
	Objects       []ObjectID
	NextToken     string
	Truncation    TruncationReason
	ResultMetrics Metrics
}

// Traverser 在给定存储上执行分页邻域遍历。
type Traverser struct {
	store    *Store
	tokenKey []byte
	pageSize int
}

// NewTraverser 创建遍历器。
func NewTraverser(store *Store) *Traverser {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// 随机源不可用属于致命环境问题。
		panic(fmt.Sprintf("ontology: cannot initialize token key: %v", err))
	}
	return &Traverser{store: store, tokenKey: key, pageSize: DefaultPageSize}
}

// Traverse 执行一次分页遍历。
//
// 拒绝次序：参数非法 > 起点无存在性权限（首次请求）/
// 续读锚点不可追溯（续读请求）> 正常分页。
func (t *Traverser) Traverse(caller *Principal, params TraverseParams) (*Page, error) {
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = t.pageSize
	}

	if params.Token != "" {
		return t.resume(caller, params, pageSize)
	}

	if params.MaxDepth < 0 || params.MaxFanout < 0 || params.Start == "" {
		return nil, ErrInvalidArgument
	}
	snap := t.store.Current()
	if !t.store.Policy().CanSeeObject(snap, caller, params.Start) {
		return nil, ErrForbidden
	}

	nb := buildNeighborhood(snap, t.store.Policy(), caller, params.Start, params.MaxDepth, params.MaxFanout)
	tok := continuationToken{
		Epoch:     snap.epoch,
		Start:     params.Start,
		MaxDepth:  params.MaxDepth,
		MaxFanout: params.MaxFanout,
		Offset:    0,
	}
	return t.slicePage(nb, tok, pageSize), nil
}

func (t *Traverser) resume(caller *Principal, params TraverseParams, pageSize int) (*Page, error) {
	tok, err := decodeToken(t.tokenKey, params.Token)
	if err != nil {
		// 续读标记格式不合法属于参数非法，判定次序最高。
		return nil, err
	}
	if params.MaxDepth < 0 || params.MaxFanout < 0 {
		return nil, ErrInvalidArgument
	}
	// 续读参数必须与首次请求一致；以不同参数“续读”没有定义，按非法处理。
	if params.MaxDepth != 0 && params.MaxDepth != tok.MaxDepth {
		return nil, ErrInvalidArgument
	}
	if params.MaxFanout != 0 && params.MaxFanout != tok.MaxFanout {
		return nil, ErrInvalidArgument
	}

	snap, ok := t.store.SnapshotAt(tok.Epoch)
	if !ok || snap == nil {
		return nil, ErrTokenObsolete
	}
	if _, exists := snap.objects[tok.Start]; !exists {
		return nil, ErrTokenObsolete
	}
	// 起点在锚定快照之后的当前状态中被删除：续读仍以快照为准，不报错。

	nb := buildNeighborhood(snap, t.store.Policy(), caller, tok.Start, tok.MaxDepth, tok.MaxFanout)
	if tok.Offset > len(nb.order) {
		return nil, ErrTokenInvalid
	}
	return t.slicePage(nb, tok, pageSize), nil
}

func (t *Traverser) slicePage(nb neighborhood, tok continuationToken, pageSize int) *Page {
	end := tok.Offset + pageSize
	if end > len(nb.order) {
		end = len(nb.order)
	}
	page := &Page{
		Objects:       append([]ObjectID(nil), nb.order[tok.Offset:end]...),
		Truncation:    nb.reason,
		ResultMetrics: nb.metrics,
	}
	if end < len(nb.order) {
		next := tok
		next.Offset = end
		encoded, err := encodeToken(t.tokenKey, next)
		if err != nil {
			// 对本结构 JSON 序列化不可能失败。
			panic(fmt.Sprintf("ontology: cannot encode token: %v", err))
		}
		page.NextToken = encoded
	}
	return page
}
