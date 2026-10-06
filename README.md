# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 能量隔离上锁挂牌工作票（`loto`）

`loto/` 是一个自包含的生产现场 LOTO（Lockout/Tagout）工作票系统，覆盖：
申请 → 审批（高风险双人去重、时段/设备冲突判定）→ 多人多锁隔离 →
零能量验证 → 开工/进出 → 完工 → 本人摘锁，以及试运行、逾期主管双人强制摘除、
只读送电判定。设计细节与取舍见 `loto/DESIGN.md`，包级用法见 `loto/doc.go`。

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./loto
go test -race ./loto

# 随机差分（真实系统 vs 朴素模型），日志打印输入/输出/判定依据
go test ./loto -run TestDifferentialRandom -loto-log=testlogs/differential.log

# 送电/冲突判定的"历史票两档"规模对照
go test ./loto -run '^$' -bench BenchmarkScaleCompare -benchtime=3000x
go test ./loto -run TestScaleFlatness -v
```

安全保证：设备依赖的任一隔离点仍有任何在位锁，或存在涉及该设备的
占用态票（开工/试运行除外，逾期除外中的"仍阻断"规则见设计文档），设备即不可送电。

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
