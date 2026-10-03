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

## srp 包：栈资源策略启动闸门

`srp/` 实现多单元资源的 Stack Resource Policy（抢占层级、动态天花板、
作业栈）。规则、拒绝原因优先级与本地验证方法见 `srp/README.md`，核心测试：

```bash
# 2000 组随机合法/非法序列与朴素模拟对拍（带逐步判定日志）
go test -run TestDifferentialNaive2000 -v ./srp
# 竞态检测
go test -race ./srp
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
