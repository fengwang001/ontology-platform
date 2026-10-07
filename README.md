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

## 快照修复与动作重放协调器

`recovery` 包实现快照对象级损坏修复与动作日志重放的原子性协调：
动作按“涉及对象集合整体可用或整体放弃”判定；损坏快照对象可由携带
完整起点的后续完整动作锚定重建（不向更早回溯）；来源分类分为
快照 / 动作重建 / 仍不可读，报告粒度为对象级。

```bash
# 若默认构建缓存只读
export GOCACHE=/tmp/gocache
go test -race -v ./recovery        # 含每次判定的输入/输出/依据日志
```

设计取舍、被放弃方案、规模无关性能论证与测试-需求对应见
[`recovery/DESIGN.md`](recovery/DESIGN.md)。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
