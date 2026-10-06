# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 最大需量控制器（`demand` 包）

工业用户最大需量控制器：按滑动窗口平均功率逼近合同需量，预测越限时按
优先级切除可控负荷，恢复时遵守最短接入/断开约束。

- 设计、关键取舍、被放弃方案与验证方法：[demand/DESIGN.md](demand/DESIGN.md)
- 朴素参考实现（保留全部历史，仅用于随机对照）：`demand/naivemodel`
- 带判定日志的演示：`go run ./demand/cmd/demo`

```bash
go test ./...                                   # 规则单测 + 随机对照
go test -race ./demand                          # 并发安全
go test ./demand/naivemodel -run TestRandomAgainstNaive -v   # 800 组随机对照
go test ./demand -run TestHistoryLengthIndependence -v       # 历史长度两档对照
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
