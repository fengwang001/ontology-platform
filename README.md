# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：能量隔离上锁挂牌（LOTO）工作票

- `loto/` — 工作票系统实现：申请、审批（高风险双批准人）、多人多锁隔离、
  零能量验证、开工、进出、完工、摘锁、试运行、逾期与强制摘除、送电判定。
- `naive/` — 独立编写的朴素参考模型（全量扫描，无索引），用于对照验证。
- `difftest/` — 随机操作序列对照测试与“历史票两档规模”性能证明。
- `docs/DESIGN.md` — 设计说明：关键取舍、被放弃的方案、本地验证方法。

```bash
go test ./...        # 全部测试
go test -race ./...  # 竞态检测
```

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
