# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `imagereclaim/`：节点镜像磁盘回收器。层按 ID 全局去重记账，支持两阶段拉取
  （`BeginPull/CommitPull/AbortPull`、原子 `Pull`）、每仓库 K 个最近使用镜像保护、
  高/低水位触发的 GC 与候选年龄过滤；并发安全、结果可精确重放。
  规则说明、拒绝码优先级与本地验证方法见 `imagereclaim/README.md`。

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
