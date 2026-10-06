package transfer

// Line 是调拨单的一行：商品与数量。同一单内商品不得重复。
type Line struct {
	Product string
	Qty     int64
}

// OrderStatus 是调拨单的生命周期状态。
type OrderStatus int

const (
	StatusCreated   OrderStatus = iota // 已创建（冻结），未发出
	StatusShipped                      // 已发出，可分批收货
	StatusClosed                       // 已关闭，短缺/盈余已登记
	StatusCancelled                    // 已取消，冻结已释放
)

func (s OrderStatus) String() string {
	switch s {
	case StatusCreated:
		return "Created"
	case StatusShipped:
		return "Shipped"
	case StatusClosed:
		return "Closed"
	case StatusCancelled:
		return "Cancelled"
	default:
		return "Unknown"
	}
}

// LineStatus 是某行的只读快照。
type LineStatus struct {
	Product  string
	Shipped  int64 // 发出量
	Received int64 // 累计收货量
	Shortage int64 // 当前短缺（关闭时登记，找回时减少）
	Surplus  int64 // 累计超收盈余
}

// Config 是系统级参数。
type Config struct {
	// OverReceiptTolerancePermille 全局超收容忍千分比，取值 [0, 1000]。
	OverReceiptTolerancePermille int64
	// CloseWaitSeconds 存在未收齐行时，自发出时刻起需等待的秒数（恰好满即可关闭）。
	CloseWaitSeconds int64
}

// StockSnapshot 是某仓某商品的只读快照。
type StockSnapshot struct {
	Available int64
	Frozen    int64
}

// ConservationEntry 是单个商品的守恒核验结果：
// Available+Frozen+InTransit+Shortage == Initial+Surplus。
type ConservationEntry struct {
	Product   string
	Available int64
	Frozen    int64
	InTransit int64
	Shortage  int64
	Surplus   int64
	Initial   int64
	Balanced  bool
}
