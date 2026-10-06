# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 室内质控模块

- `qc.NewSystem()` 创建线程安全的质控与报告系统。
- `RegisterAssay` 登记仪器-项目的低/高水平靶值、标准差和有效期。
- `RunQC` 同时提交两个水平测量值，返回正常、警告或失控，以及按顺序触发的五条规则。
- `Calibrate` 清空两个水平的连续运行序列并立即恢复在控，不修改靶值、标准差和已出具报告。
- `IssueReport` 在项目在控、存在非失控运行且未超过有效期时出具报告。
- `ReviewReport` 只允许将待复核报告转为已复核。
- `AssaySnapshot`、`LastRun` 和 `ReportStatus` 是不推进时钟的只读快照。

详细设计见 `docs/design.md`。

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

### 室内质控专项

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache-ontology
go test ./...
go test -race ./qc
go test ./qc -run TestRandomComparisonAgainstNaiveModel -count=1 -v | tee /tmp/qc-random.log
go test ./qc -run TestScaling -count=1 -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
