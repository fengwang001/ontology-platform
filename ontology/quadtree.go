package ontology

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Point 是一个带编号的二维整数点。
type Point struct {
	ID string
	X  int64
	Y  int64
}

// Rect 是左闭右开矩形 [X0,X1) x [Y0,Y1)。
type Rect struct {
	X0, Y0, X1, Y1 int64
}

// QueryStats 报告一次查询访问索引的方式，用于验证剪枝效果。
type QueryStats struct {
	// PointChecks 是真正逐点判定（点是否落在查询矩形内）的次数。
	PointChecks int
	// FullyContainedLeaves 是被“整格包含”直接收录的叶子格数。
	FullyContainedLeaves int
	// BoundTestedNodes 是与查询矩形做过包含/相交几何判定的格节点数。
	BoundTestedNodes int
	// PrunedNodes 是因与查询矩形不相交而被整体剪枝的格节点数。
	PrunedNodes int
}

// QueryResult 是一次查询的结果。
type QueryResult struct {
	IDs   []string
	Stats QueryStats
}

// IndexStats 是索引当前结构快照。
type IndexStats struct {
	PointCount    int
	NodeCount     int
	LeafCount     int
	OverflowCells int // 边长为 1、点数超过容量的格数
	MaxDepth      int
}

// Option 配置 Index。
type Option func(*Index)

// WithLogger 设置结构化日志；nil 表示丢弃日志。
func WithLogger(logger *slog.Logger) Option {
	return func(idx *Index) { idx.logger = logger }
}

// Index 是可自动分裂的二维点四叉树索引。
type Index struct {
	mu       sync.RWMutex
	originX  int64
	originY  int64
	side     int64
	capacity int

	root   *node
	points map[string]Point

	logger *slog.Logger
}

// New 创建根区域 [originX, originX+side) x [originY, originY+side) 的索引。
// side 必须是正的 2 的幂，capacity 必须为正，否则返回可区分的错误。
func New(originX, originY, side int64, capacity int, opts ...Option) (*Index, error) {
	if side <= 0 || side&(side-1) != 0 {
		return nil, newIndexError(ErrInvalidSize.Kind(),
			"root side %d is not a positive power of two", side)
	}
	if capacity <= 0 {
		return nil, newIndexError(ErrInvalidCapacity.Kind(),
			"capacity %d must be positive", capacity)
	}
	idx := &Index{
		originX:  originX,
		originY:  originY,
		side:     side,
		capacity: capacity,
		root:     newLeaf(0),
		points:   make(map[string]Point),
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(idx)
	}
	if idx.logger == nil {
		idx.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	idx.logger.Info("index created",
		slog.Int64("originX", originX), slog.Int64("originY", originY),
		slog.Int64("side", side), slog.Int("capacity", capacity))
	return idx, nil
}

// Insert 批量插入点。任何一个点非法（越界、编号空、编号重复）都整体拒绝，
// 索引结构与已有数据保持不变。插入后逐格按需四等分。
func (idx *Index) Insert(points []Point) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for i, p := range points {
		if p.ID == "" {
			err := newIndexError(ErrEmptyID.Kind(), "point at batch position %d has empty id", i)
			idx.logger.Warn("insert rejected", slog.String("reason", err.Error()), slog.Int("batch", len(points)))
			return err
		}
		if _, exists := idx.points[p.ID]; exists {
			err := newIndexError(ErrDuplicateID.Kind(), "id %q already exists", p.ID)
			idx.logger.Warn("insert rejected", slog.String("reason", err.Error()), slog.Int("batch", len(points)))
			return err
		}
		if !idx.containsRoot(p) {
			err := newIndexError(ErrPointOutOfRange.Kind(),
				"point %q (%d,%d) outside root [%d,%d)x[%d,%d)",
				p.ID, p.X, p.Y, idx.originX, idx.originX+idx.side, idx.originY, idx.originY+idx.side)
			idx.logger.Warn("insert rejected", slog.String("reason", err.Error()), slog.Int("batch", len(points)))
			return err
		}
	}
	seen := make(map[string]struct{}, len(points))
	for _, p := range points {
		if _, dup := seen[p.ID]; dup {
			err := newIndexError(ErrDuplicateID.Kind(), "id %q repeats within batch", p.ID)
			idx.logger.Warn("insert rejected", slog.String("reason", err.Error()), slog.Int("batch", len(points)))
			return err
		}
		seen[p.ID] = struct{}{}
	}

	for _, p := range points {
		idx.points[p.ID] = p
		idx.root.insert(p, idx.originX, idx.originY, idx.side, idx.capacity)
	}
	idx.logger.Info("insert accepted", slog.Int("inserted", len(points)),
		slog.Int("total", len(idx.points)))
	return nil
}

// Delete 按编号批量删除。任何一个编号不存在（或批次内重复）都整体拒绝。
// 删除不触发合并。
func (idx *Index) Delete(ids []string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			err := newIndexError(ErrDeleteNotFound.Kind(), "empty id cannot exist")
			idx.logger.Warn("delete rejected", slog.String("reason", err.Error()), slog.Int("batch", len(ids)))
			return err
		}
		if _, dup := seen[id]; dup {
			err := newIndexError(ErrDeleteNotFound.Kind(), "id %q repeats within delete batch", id)
			idx.logger.Warn("delete rejected", slog.String("reason", err.Error()), slog.Int("batch", len(ids)))
			return err
		}
		seen[id] = struct{}{}
		if _, ok := idx.points[id]; !ok {
			err := newIndexError(ErrDeleteNotFound.Kind(), "id %q does not exist", id)
			idx.logger.Warn("delete rejected", slog.String("reason", err.Error()), slog.Int("batch", len(ids)))
			return err
		}
	}

	for _, id := range ids {
		p := idx.points[id]
		idx.root.remove(id, p.X, p.Y, idx.originX, idx.originY, idx.side)
		delete(idx.points, id)
	}
	idx.logger.Info("delete accepted", slog.Int("deleted", len(ids)),
		slog.Int("total", len(idx.points)))
	return nil
}

// Query 返回落在左闭右开矩形 r 内的点编号（升序）及剪枝统计。
// 宽或高为零返回空结果；左端大于右端整体拒绝。
func (idx *Index) Query(ctx context.Context, r Rect) (QueryResult, error) {
	if r.X1 < r.X0 || r.Y1 < r.Y0 {
		err := newIndexError(ErrInvalidQueryRange.Kind(),
			"query [%d,%d)x[%d,%d) has lower bound greater than upper bound",
			r.X0, r.X1, r.Y0, r.Y1)
		idx.logger.Warn("query rejected", slog.String("reason", err.Error()))
		return QueryResult{}, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	res := QueryResult{IDs: []string{}}
	if r.X0 == r.X1 || r.Y0 == r.Y1 {
		idx.logger.Info("query empty (zero width/height)",
			slog.Int64("x0", r.X0), slog.Int64("x1", r.X1),
			slog.Int64("y0", r.Y0), slog.Int64("y1", r.Y1),
			slog.Int("hits", 0))
		return res, nil
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	res = idx.queryLocked(r)

	idx.logger.Info("query served",
		slog.Int64("x0", r.X0), slog.Int64("x1", r.X1),
		slog.Int64("y0", r.Y0), slog.Int64("y1", r.Y1),
		slog.Int("hits", len(res.IDs)),
		slog.Int("pointChecks", res.Stats.PointChecks),
		slog.Int("fullyContainedLeaves", res.Stats.FullyContainedLeaves),
		slog.Int("boundTestedNodes", res.Stats.BoundTestedNodes),
		slog.Int("prunedNodes", res.Stats.PrunedNodes),
		slog.String("ids", summarizeIDs(res.IDs)))
	return res, nil
}

// Stats 返回当前结构统计。
func (idx *Index) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	nodes, leaves, maxDepth := idx.root.countNodes()
	return IndexStats{
		PointCount:    len(idx.points),
		NodeCount:     nodes,
		LeafCount:     leaves,
		OverflowCells: idx.root.countOverflow(idx.side, idx.capacity),
		MaxDepth:      maxDepth,
	}
}

func (idx *Index) containsRoot(p Point) bool {
	return idx.originX <= p.X && p.X < idx.originX+idx.side &&
		idx.originY <= p.Y && p.Y < idx.originY+idx.side
}

// queryLocked 在已持有读锁（或独占锁）时执行四叉树查询。
func (idx *Index) queryLocked(r Rect) QueryResult {
	res := QueryResult{IDs: []string{}}
	ids := make([]string, 0)
	idx.root.collect(idx.originX, idx.originY, idx.side, r, &res.Stats, &ids)
	sort.Strings(ids)
	res.IDs = ids
	return res
}

// naiveQueryLocked 在已持有读锁时逐点扫描，作为查询正确性的参照。
func (idx *Index) naiveQueryLocked(r Rect) []string {
	ids := make([]string, 0)
	for _, p := range idx.points {
		if r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1 {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// snapshotPoints 返回当前全部点的副本；仅供测试与朴素扫描对拍使用。
func (idx *Index) snapshotPoints() []Point {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]Point, 0, len(idx.points))
	for _, p := range idx.points {
		out = append(out, p)
	}
	return out
}

// naiveQuery 在调用时刻的点集上逐点扫描，是查询正确性的参照实现。
// 调用方不持锁；本方法自行获取读锁，因此可与写操作并发。
func (idx *Index) naiveQuery(r Rect) []string {
	pts := idx.snapshotPoints()
	ids := make([]string, 0)
	for _, p := range pts {
		if r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1 {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func summarizeIDs(ids []string) string {
	const limit = 10
	if len(ids) <= limit {
		return strings.Join(ids, ",")
	}
	return strings.Join(ids[:limit], ",") + ",...(+" + itoa(len(ids)-limit) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
