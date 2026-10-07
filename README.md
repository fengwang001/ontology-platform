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

## 对象生命周期状态机子系统

`lifecycle` 包实现到期迁移的**惰性结算**状态机（不依赖后台扫描），`naive` 包是独立
实现的朴素周期扫描参考模型，二者通过随机对拍保持一致。

- 设计说明（关键取舍、被放弃方案、本地验证方法）：`docs/lifecycle-design.md`
- 核心实现：`lifecycle/`
- 参考模型：`naive/`
- 脚本化演示（打印每次结算输入、推进环节、判定依据）：

```bash
go run ./cmd/lifecycle-demo
```

随机对拍与开销证明测试：

```bash
go test -race -v ./...
```
