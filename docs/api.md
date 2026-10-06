# API 文档

包路径：`ontology/speq`。所有方法可并发调用。

## 类别配置

```go
type CategoryConfig struct {
    Code            string     // 类别编码（唯一）
    Kind            ObjectKind // KindDevice / KindSafetyValve / KindPressureGauge
    PeriodMonths    int        // 检验周期（月数，>0）
    EarlyWindowDays int        // 提前检验窗口（天，含边界）
    MinUnsealDays   int        // 启封最小保障天数
    WarningLeadDays int        // 预警提前天数
}
func (s *System) AddCategory(cfg CategoryConfig) error
```

## 登记

```go
func (s *System) RegisterDevice(date int, id, category string, firstPassDate int) (expiry int, err error)
func (s *System) RegisterAttachment(date int, id, category string, kind ObjectKind, firstPassDate int) (expiry int, err error)
```

到期日 = 首次检验合格日 + 一个检验周期（日历月）。

## 检验

```go
type InspectionResult int // ResultPass / ResultConditional / ResultFail
func (s *System) Inspect(date int, id string, result InspectionResult, rectifyDays int) (newExpiry int, err error)
```

- 合格：窗口内以原到期日为基准，否则以检验日为基准，加一个周期。
- 有条件合格：`min(合格规则结果, 检验日 + rectifyDays)`。
- 不合格：立即停用，到期日不变。
- 封存状态可检验；启封时只顺延检验之后经过的封存天数。

## 封存 / 启封

```go
func (s *System) Seal(date int, id string) error
func (s *System) Unseal(date int, id string) (newExpiry int, err error)
```

封存要求未超期、未停用。启封后剩余有效天数不足最小保障时返回
`ErrConditionNotMet`，须先在封存状态检验。

## 附件挂接 / 转移

```go
func (s *System) MountAttachment(date int, attachmentID, deviceID string) error
```

附件当前未超期、未封存、未停用方可挂接/转移；已在目标设备上时幂等成功。

## 使用登记 / 可使用性

```go
func (s *System) RegisterUse(date int, deviceID string) error
func (s *System) CheckUsable(date int, deviceID string) (UsableReport, error)
```

`RegisterUse` 被拒绝时错误为 `*RejectError`：

| Reason | 含义 |
| --- | --- |
| `RejectDeviceSealed` | 设备自身封存 |
| `RejectDeviceDisabled` | 设备自身停用 |
| `RejectDeviceExpired` | 设备自身超期 |
| `RejectNoSafetyValve` | 未挂接任何安全阀 |
| `RejectAttachment` | 附件不满足（`Attachment` 为编号最小者） |

`CheckUsable` 为只读判定，不推进操作日期。

## 预警

```go
type WarningEntry struct {
	ID         string
	Kind       ObjectKind
	Expiry     int      // 对象自身到期日
	SortExpiry int      // 排序键（自身命中时为自身到期日；附件触发时取最早）
	Triggers   []string // 设备条目：触发的附件编号（升序）
}
func (s *System) QueryWarnings(date int) ([]WarningEntry, error)
```

返回当日起各类别预警窗口内到期、未封存、未停用的对象；
设备因附件临近到期时与自身命中合并为一条，`Triggers` 给出触发附件。
排序：`SortExpiry` 升序，并列按编号升序。

## 报废

```go
func (s *System) Scrap(date int, id string) error
```

设备报废自动脱离全部附件；附件报废自动摘除。报废后拒绝一切操作。

## 只读辅助

```go
func (s *System) Get(id string) (Snapshot, bool)
func (s *System) Snapshot() []Snapshot          // 全部对象，编号升序
func (s *System) AttachmentsOf(deviceID string) []string
func (s *System) LastDate() int                 // 上一个被接受操作日期
func (s *System) IndexSize() int                // 到期索引条目数
func (s *System) IndexDepth() int               // treap 高度（验证平衡）
```

## 日历

```go
func DateToOrdinal(year, month, day int) (int, bool)
func OrdinalToDate(ordinal int) (year, month, day int, ok bool)
func AddCalendarMonths(ordinal int, months int) int
```

## 错误处理

操作错误为 `*OpError`，其 `Code` 依次为：
`ErrInvalidParameter < ErrDateRegression < ErrNotFound < ErrScrapped
< ErrIllegalState < ErrConditionNotMet`，可用 `errors.As` 提取。
