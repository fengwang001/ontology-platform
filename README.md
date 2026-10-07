# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `dtc`：车载诊断故障码（DTC）生命周期管理器。根据监测结果与点火/暖机循环事件维护
  每个故障码的待定、确认、愈合与清除，并管理全车唯一的冻结帧槽位。设计说明见
  [docs/design.md](docs/design.md)。
- `dtctest`：独立编写的朴素参考模型，用于与 `dtc` 做随机事件序列对照测试。

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
