# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## swingdoor：流式旋转门趋势压缩器

`swingdoor` 包逐点接收时序采样并只保留必要的存档点，使任一被丢弃点到其前后相邻存档点连线的纵向偏差都不超过容差 `E`。相同输入序列重放得到逐位相同的存档点序列。

### 斜率上下界与重启规则

- 第一个采样即存档点并成为当前起点 `(t0, v0)`，此时 `lo = -∞`、`hi = +∞`，待定点集合为空。
- 每个后续采样 `(t, v)` 先算相对起点的斜率 `s = (v-v0)/(t-t0)`：
  - 若待定点集合为空，或 `lo <= s <= hi`（恰等于 `lo` 或 `hi` 仍算可继续），该采样成为待定点，并收紧上下界：
    `lo = max(lo, (v-E-v0)/(t-t0))`，`hi = min(hi, (v+E-v0)/(t-t0))`。
  - 否则「上一个」采样（最近一个待定点）成为存档点并成为新起点，此前待定点全部丢弃；新采样相对新起点重新计算，成为新起点后的第一个待定点，`lo`/`hi` 只由它自己决定。
- 关闭时若最后一个采样尚非存档点则追加为存档点；首尾采样必在存档点中，存档点时间戳严格递增。
- 所有分数比较一律用整数交叉相乘，不用浮点：乘积不超过 int64 时直接比较，否则自动退化为 `big.Int`，保证精确与确定性。

### 拒绝原因与检查顺序

所有被拒绝的操作都不改变存档点、待定点与上下界。检查顺序为 **已关闭 → 数值超限 → 时间次序**，原因可区分：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidTolerance` | 容差为负或超过 `MaxTolerance`（4e9） |
| `ErrAlreadyClosed` | 关闭后再写入 / 重复关闭 |
| `ErrValueOutOfRange` | 时间戳或数值绝对值超过 `MaxAbsValue`（1e9） |
| `ErrTimestampEqual` | 时间戳等于上一采样 |
| `ErrTimestampBackward` | 时间戳小于上一采样 |
| `ErrEmptyStream` | 空流关闭 |

### 并发

`Write`、`Close`、`Points` 均可并发调用；内部以互斥串行化，结果等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含有理数朴素校验：逐点验证每个被丢弃点相对相邻
# 存档点连线的偏差不超过 E，日志打印输入、输出与判定依据）
go test -v ./swingdoor/

# 竞态检测
go test -race ./swingdoor/
```
