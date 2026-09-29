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

## 等宽增量直方图（`histogram` 包）

`histogram` 提供并发安全的等宽分桶增量直方图：加值计数加一，撤值减一，
计数减到零时桶从直方图消失（查询返回“不存在”而非零）。

### 桶归属与溢出桶边界规则

- 值域 `[0, upper)` 按桶宽 `width` 等宽切分，`upper` 必须为正且是 `width` 的整数倍。
- 桶左闭右开：第 `i` 桶覆盖 `[i*width, (i+1)*width)`；值恰好等于某桶左边界归该桶，
  等于右边界则归下一桶。
- 达到或超过 `upper` 的值进独立溢出桶（下标为 `upper/width`，区间记为 `[upper, +∞)`），
  与常规桶互不影响。
- 负值非法；构造参数非法、撤回不存在的桶、活跃桶数超限都会整体拒绝，
  错误可用 `errors.Is` 区分（如 `histogram.ErrNegativeValue`），失败不改变直方图。

### 本地验证：正负相抵后分组计数核对

对同一批值先加后撤（正负相抵），直方图应回到空；再用分组计数核对每桶计数：

```bash
# 竞态检测 + 详细日志（打印输入值、桶归属、计数与判定依据）
go test -race -v ./histogram
```

手工核对方法：

1. 把输入值流写成带符号事件：`+v` 表示 `Add(v)`，`-v` 表示 `Remove(v)`。
2. 按桶分组：桶下标 `i = v / width`（`v >= upper` 归溢出桶，`v < 0` 应被拒绝）。
3. 组内正负相抵求代数和，得到每桶期望计数；和为零的桶应不存在。
4. 与 `Buckets()` 返回的快照逐字段比对，应完全一致。
