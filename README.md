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

## 登机口分配系统（`gate` 包）

- `gate/model.go`：领域类型、统一拒绝次序、四级优先级、左闭右开区间规则。
- `gate/tree.go`：每登机口一棵增广 treap（子树 `maxEnd` 剪枝）。
- `gate/system.go`：互斥锁串行化的正式实现：`Assign` / `Delay` / `OccupantAt` / `Snapshot`。
- `gate/naive.go`：线性扫描的独立朴素模型，仅供随机对拍。
- `cmd/gatesim`：确定性演示脚本。

详见 `DESIGN.md`。

```bash
# 规则测试 + 随机/对抗对拍
go test ./gate/ -v

# 竞态检测
go test -race ./gate/

# 只看对拍每步输入、输出与判定理由
go test ./gate/ -run TestRandomDifferential -v

# 演示
go run ./cmd/gatesim
```
