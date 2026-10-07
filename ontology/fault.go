package ontology

// CutPoint 枚举处理单元内可被模拟中断的切分点。
type CutPoint int

const (
	// CutAfterWALBegin undo 记录已写入 WAL、尚未修改任何数据之后。
	CutAfterWALBegin CutPoint = iota
	// CutAfterValue 属性值已改、索引条目尚未改之后。
	CutAfterValue
	// CutAfterIndex 某个索引结构已更新、其余索引结构尚未更新之后（按索引逐个触发）。
	CutAfterIndex
	// CutAfterAllIndexes 全部索引结构已更新、提交记录尚未写入之后。
	CutAfterAllIndexes
	// CutAfterCommit 提交记录已写入之后（恢复时应前滚而非回滚）。
	CutAfterCommit
)

func (c CutPoint) String() string {
	switch c {
	case CutAfterWALBegin:
		return "after-wal-begin"
	case CutAfterValue:
		return "after-value"
	case CutAfterIndex:
		return "after-index"
	case CutAfterAllIndexes:
		return "after-all-indexes"
	case CutAfterCommit:
		return "after-commit"
	default:
		return "unknown"
	}
}

// crashSignal 是崩溃模拟用的 panic 载荷，表示处理单元在切分点处中断。
type crashSignal struct {
	point   CutPoint
	txnID   uint64
	indexID string
}

// FaultHook 在指定切分点被调用；返回 true 表示在此注入崩溃。
// indexID 仅在 CutAfterIndex 切分点有意义（刚更新完的索引结构）。
type FaultHook func(point CutPoint, txnID uint64, indexID string) bool

// failIndexes 记录被强制维护失败的索引结构 ID（模拟索引维护失败错误）。
type FaultInjector struct {
	crashAt       FaultHook
	failIndex     map[string]bool
	failIndexInst map[string]string
	crashed       []crashSignal
}

func NewFaultInjector() *FaultInjector {
	return &FaultInjector{failIndex: make(map[string]bool), failIndexInst: make(map[string]string)}
}

// CrashWhen 设置崩溃注入条件。
func (f *FaultInjector) CrashWhen(h FaultHook) { f.crashAt = h }

// FailIndex 强制指定索引结构的维护失败。
func (f *FaultInjector) FailIndex(indexID string) { f.failIndex[indexID] = true }

// FailIndexFor 强制指定索引结构仅对某个实例的维护失败。
func (f *FaultInjector) FailIndexFor(indexID, instanceID string) {
	f.failIndexInst[indexID] = instanceID
}

// Reset 清除全部注入配置与崩溃记录。
func (f *FaultInjector) Reset() {
	f.crashAt = nil
	f.failIndex = make(map[string]bool)
	f.failIndexInst = make(map[string]string)
	f.crashed = nil
}

// shouldFailIndex 判定指定索引结构对某个实例的维护是否应失败。
func (f *FaultInjector) shouldFailIndex(indexID, instanceID string) bool {
	if f.failIndex[indexID] {
		return true
	}
	inst, ok := f.failIndexInst[indexID]
	return ok && inst == instanceID
}

// Crashed 返回已注入的崩溃记录（测试断言用）。
func (f *FaultInjector) Crashed() []crashSignal { return f.crashed }

func (f *FaultInjector) maybeCrash(point CutPoint, txnID uint64, indexID string) {
	if f.crashAt != nil && f.crashAt(point, txnID, indexID) {
		sig := crashSignal{point: point, txnID: txnID, indexID: indexID}
		f.crashed = append(f.crashed, sig)
		panic(sig)
	}
}
