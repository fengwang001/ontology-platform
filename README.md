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

## 子系统：动作假设性重新预检（`precheck`）

支持对历史时刻的动作调用做只读的假设性重新演算，重建当时生效的钩子版本集合与权限继承快照，
给出允许/拒绝结论与状态改变意图，且结果可与独立朴素重演模型逐条对照。

- 代码：`precheck/`（引擎、时态存储、两阶段钩子、权限快照、审计、朴素重演）
- 演示：`go run ./cmd/precheck-demo`
- 设计说明：[`docs/precheck-design.md`](docs/precheck-design.md)（关键取舍、边界规则、
  错误优先级、失败聚合规则、复杂度论证与被放弃方案）
- 测试：边界时刻穷举、前后置聚合规则、四类错误优先级、只读保证、
  随机操作序列双实现差分对照、快照重建复杂度计数器验证。
