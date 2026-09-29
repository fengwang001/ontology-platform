# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 原子批次物化器（`ontology` 包）

`ontology.Materializer` 是一个全或无（all-or-nothing）的键值批次物化器：
一个批次是一组按顺序排列的 `Event`，要么整批一次性生效，要么整批不留痕迹。

### 事件与前置期望

每个 `Event` 针对一个键执行一次变更，并携带前置期望：

- `Op: Write`：把键写成 `Value`；`Op: Delete`：删除该键。
- `Expect == nil`：要求该键**当前不存在**。
- `Expect != nil`：要求该键当前值与 `*Expect` **完全相等**。

### 批内预演规则

`Apply(batch)` 严格按批内下标顺序逐条预演：

1. 第 `i` 条事件的判定视图 = 已提交的真实视图 **叠加本批前 `i` 条事件的效果**；
   前序写入的新值、前序删除造成的缺失都对后续事件可见。
2. 前置期望满足才把该事件的效果写入预演工作副本；遇到**第一条**不满足的事件
   立即停止并拒绝整批，错误定位到该事件的下标。
3. 非法输入在预演到该位置时拒绝，三类错误互不相同，可用 `errors.Is` 判定：
   - `ErrEmptyBatch`：空批（`nil` 或长度为 0）；
   - `ErrEmptyKey`：事件键为空；
   - `ErrInvalidOp`：事件操作既非 `Write` 也非 `Delete`；
   - 前置期望失败为 `ErrPrecondition`，以 `*BatchError` 返回，含 `Index/Key/
     Expected/Actual`，可用 `errors.As` 取出并定位第一条失败事件。

### 原子生效语义

- 预演在私有工作副本上进行；任一条失败则工作副本被丢弃，已提交视图字节级不变，
  被拒后可继续正常提交后续批次。
- 全部通过后，结果视图通过一次原子指针发布（`atomic.Pointer[map[string]string]`）
  整体生效。下游 `Get`/`Snapshot`/`Check` 为无锁读，与 `Apply` 可并发；任一读到的
  视图都对应某个**完整批次边界**（整批前或整批后），不存在半个批次的中间态。
- `Apply` 之间串行化；`Generation()` 在每次成功提交后恰好加 1，可复现地标识批次边界。
- 已发布的 map 永不原地修改；`Snapshot` 返回独立拷贝，调用方可自由改动。

### 日志

构造时可用 `ontology.WithLogger(w)` 指定输出（`nil` 关闭）。每次提交都会打印：
批输入（逐条 `op/key/value/expect`）、每条事件的 `expect/observed/match` 判定依据、
以及最终 `committed`（含 generation）或 `rejected`（含失败下标与“no state change”）。

### 快速示例

```go
m := ontology.New()
err := m.Apply([]ontology.Event{
    {Key: "k", Op: ontology.Write, Value: "v1", Expect: nil}, // 要求不存在
    {Key: "k", Op: ontology.Write, Value: "v2", Expect: sp("v1")}, // 看到批内新值
    {Key: "k", Op: ontology.Delete, Expect: sp("v2")},        // 批内先写后删
})
// err == nil；整批一次生效，k 最终不存在
```

可运行演示（打印输入、逐条判定依据与结果）：

```bash
go run ./cmd/demo
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 拉取依赖
go mod tidy

# 全量测试
go test ./...

# 带竞态检测与详细输出（含输入/结果/判定依据日志）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -race -run TestApply_ConcurrentReadersSeeBoundaries -v ./ontology

# 演示全或无与批内顺序
go run ./cmd/demo

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...
```

测试覆盖场景：

- 批内顺序：同键先写后删、链式期望（后一条看到前一条写入的值）；
- 全或无：批中段失败导致前后事件全部不生效，状态逐键校验不变；
- 失败定位：报告的错误始终对应第一条失败事件（后面的失败不报告）；
- 三类非法输入：空批、空键、非法操作分别命中不同错误，拒绝后状态不变且可继续使用；
- 并发：8 个读者在 200 个批次提交期间持续校验“只见完整批次边界”，
  以及多个并发 `Apply` 下视图与 generation 不损坏（`-race` 下验证）。
