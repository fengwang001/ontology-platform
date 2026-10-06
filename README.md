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

## 拥堵收费与豁免结算

包 `congestion` 在仓库根包中提供内存态、可并发调用的结算服务：

- `New`：配置本地时区、日封顶、最小货币单位倍数和追溯天数。
- `AddZone`：新增区域；矩形相交但不构成嵌套时返回 `ErrInvalidNesting`。
- `RegisterVehicle`、`ChangePlate`：管理随时间生效的车牌归属。
- `RecordEntry`：登记进入；进入内层会同时登记所有外层。
- `RegisterEntitlement`：登记居民、残障或新能源资格，可返回追溯退款调整。
- `OpenDispute`、`CloseDispute`：冻结某日金额，关闭时一次性重算并入一笔调整。
- `Report`：按车辆和 `YYYY-MM-DD` 读取应付、冻结态、调整及逐区域判定证据。

所有金额使用 `int64` 最小货币单位；居民折扣用基点 `DiscountBasisPts` 表示，减免后向上取整。所有写操作携带单调不回退的全局操作时刻。调整中 `Delta < 0` 表示退款，`Before/After` 可直接复算差额。

随机对照测试会打印每条操作的输入、输出与判定依据：

```bash
go test -run TestRandomOperationsMatchNaiveReplay -v
```

边界覆盖包含收费窗端点、同刻内外层顺序、精确触顶、三类资格并存、折扣取整后触顶、追溯截止时刻、车牌切换时刻和争议关闭一次性调整。设计取舍见 `DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
