# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多实例资源死锁检测与消解

`deadlock` 包实现带备选请求（“任选其一”、按列出顺序取第一个可满足备选）的
多实例资源分配、bseq 先到先授予不动点、图归约死锁检测（`checks ≤ b(b+1)/2`）
与按 `Σ alloc·c·(1+rb)` 选最小代价牺牲者的原子消解。规则推导、净增量说明、
牺牲者选择与本地验证方法见 `deadlock/DESIGN.md`。

```bash
go test ./...
go test -race ./...
go test -run TestRandomDifferential -v ./deadlock  # 2000 组随机序列 + 朴素 oracle 对照日志
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
