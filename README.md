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

## 合同版本叠加与自动续签服务

包 `contract/` 管理主合同与补充协议的签署、生效、撤销与终止，
支持按任意整数日序号查询每条条款的有效值及其来源、是否在期、
当前到期日与全部续签记录；并发调用等价于某个串行顺序，
相同操作序列重放结果完全一致。

```bash
# 全部测试（定向边界 + 朴素模型随机对照 + 并发等价 + 查询开销证明）
go test ./contract/ -count=1

# 竞态检测
go test ./contract/ -race -count=1

# 随机对照的逐步日志（输入、输出与判定依据）
go test ./contract/ -run 'TestRandomDifferential/seed=1' -v
```

设计取舍、被放弃的方案与本地验证方法见 [contract/DESIGN.md](contract/DESIGN.md)。
