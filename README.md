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

## 光伏余电上网月度结算引擎（`settlement` 包）

位于 `settlement/`，设计与取舍见 `settlement/DESIGN.md`（≤40 行）。

```go
eng := settlement.NewEngine(3600, time.Date(2026,1,1,0,0,0,0,time.UTC))
eng.RegisterProsumer("p1")
eng.SetParams("p1", "2026-01", settlement.Params{ContractPowerW: 5000, MonthlyCreditableW: 1_000_000, CreditValidMonths: 2})
eng.SetPrices("p1", "2026-01", settlement.Prices{ImportPricePerWh: 10, SurplusPricePerWh: 4})
eng.RegisterReading("p1", settlement.Reading{Start: tsUTC, ImportWh: 30, ExportWh: 70})
res, err := eng.CloseMonth("p1", "2026-01")      // 封账
res, err = eng.Query("p1", "2026-02")            // 未封账月试算，不改额度余额
```

- 错误：`errors.Is(err, settlement.ErrInvalid|ErrClosed|ErrOrder|ErrMissing)`，拒绝次序固定。
- 测试与验证：
  - `go test -race ./settlement`：全部边界用例 + 朴素模型随机差分（固定种子可复现）。
  - `SETTLE_LOG=1 go test ./settlement -run TestRandomDifferential -v`：打印每条输入、双方输出与判定依据。
  - `go test ./settlement -run TestCloseScaling -v`：封账开销不随已封账月数增长的实测证据。
