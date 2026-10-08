# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `dtc`：车载诊断故障码（DTC）生命周期管理器 —— 去抖、待定/确认/愈合/清除、
  暖机循环判定、唯一冻结帧槽位与诊断仪清除。设计与验证方法见
  [dtc/DESIGN.md](dtc/DESIGN.md)。

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
