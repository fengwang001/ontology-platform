# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 影像检查预约与对比剂准入系统（`imaging` 包）

`imaging/` 实现 CT / 磁共振预约与对比剂准入：设备时段与清洁、每日重复质控、
肾功能结果时效、植入物场强兼容、留观位容量、签到复核与改约/取消。

- 设计与取舍、被放弃方案、复杂度证明：[`imaging/DESIGN.md`](imaging/DESIGN.md)
- 入口类型与 API：`imaging/imaging.go`；错误枚举：`imaging/errors.go`
- 独立朴素参考模型与差分：`imaging/naive*.go`、`imaging/diff_*_test.go`
- 快速验证：

```bash
go test ./...                                   # 全量测试
DIFF_LOG=1 go test ./imaging -run TestDifferential1500 -v   # 1800 步随机差分日志
go test ./imaging -run '^$' -bench 'Benchmark.*History' -benchmem  # 两档规模对照
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
