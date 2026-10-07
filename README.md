# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `adjudicator`：备份重建裁决组件。判定四类备份（对象类型定义、对象实例、
  链接实例、动作执行记录）的唯一重建顺序与可重建范围，处理整体缺失、
  部分损坏级联、循环依赖与重建中途新发现的损坏。
  设计说明见 [docs/adjudication-design.md](docs/adjudication-design.md)。

```go
snap := &adjudicator.Snapshot{ /* 四类备份状态 */ }
v := adjudicator.New().Adjudicate(snap)
// v.Order     唯一确定的重建顺序
// v.Verdicts  每条记录的可重建判定与原因
// v.Log       判定依据
// v.Err       最高优先级的裁决错误

sess := adjudicator.NewSession(snap) // 执行期：支持中途新发现损坏的停止与重评估
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
