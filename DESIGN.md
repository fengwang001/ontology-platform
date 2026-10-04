# 成交价格闸门 设计说明

## 结构
- `band`：纯标的参数与价格数学（无锁）。`Symbol` 存 `prev,L,Ds,Dd,De,W,T,X,Hmax,open,close,C` 及预算的 `up,dn`；提供涨跌停与超带判定（`|x-R|*10000 > D*R`，先乘后比，避免浮点）。
- `phase`：单标的阶段机（无锁）。状态 `cont`/`halt`，保存 `he`、已延长次数、当日中断次数；方法包含进入中断、延长、恢复成交。
- `gate`：门面。`Gate` 持 `sync.Mutex` 与 `map[string]*symState`，串行化所有操作即满足并发等价于某串行顺序；维护全局单调时钟 `maxNow`。

## 关键取舍
- 动态参考价定义为“成交时刻不晚于 `now-W` 的最后一笔”，即窗口之前（含边界）的最后一笔，而非最近一笔成交；取窗口内最近成交是被放弃的语义（会与示例 `Trade(11)` 取 1010 矛盾）。
- 中断到点不自动恢复：即使 `now>=he`，未调用 `Resume` 前 `Trade` 仍报 `ErrHalted`；状态机只有显式 `Resume` 才能离开 halt。
- 拒绝顺序固定：参数 > 时钟回退 > 标的（Add 的重复在此层）> 阶段/已中断 > 涨跌停 > 超带；被拒操作不触碰任何状态（含时钟与计数器）。
- 尾盘与 Hmax 用尽时超带只返回超带错误，不进中断；`he` 用 `min(now+T, close-C)` 截断，保证不越过尾盘起点；延长同样受三重条件（De 超带、次数<X、`he<close-C`）约束。
- `Resume` 成交时清空成交记录、`Rs` 置为成交价、记录中只留本笔；此后窗口内无“旧”成交，Rd 在新成交老化前回落 Rs。

## 数据结构与复杂度
- 成交记录用切片队首游标 `(trades []tick, head int)`，时间单调故老化记录恒在队首；另存 `lastRd`（上次弹出的最后一笔价，初值 Rs）。
- 求 Rd 时循环弹出 `time <= now-W` 的队首记录并更新 `lastRd`，每条记录至多弹出一次，`popped` 计数 ≤ 成交总数；单次 Trade 摊还 O(1)，与窗口内笔数无关。朴素模拟每次线性扫描全部记录，用于 1500 组随机序列对照。
- 时间与金额统一 `int64`：价 ≤1e9、基点 ≤1e4，乘积 ≤2e13，远在 int64 内，杜绝溢出与浮点误差。

## 错误模型
- 哨兵：`ErrParam, ErrClock, ErrSymbol, ErrPhase, ErrHalted, ErrLimit, ErrOverBand`；静态/动态超带分别 `fmt.Errorf("%w: ...", ErrStatic/ErrDynamic)`，二者均被 `errors.Is(err, ErrOverBand)` 命中，可精确区分原因。

## 本地验证
- `go build ./...`；`go test ./... -v`（表驱动用例 + 1500 组随机序列朴素对照 + popped 上限/摊还与 10/10000 两档对照）；`go test -race ./...`；`go vet ./...`；`gofmt -l .`。
