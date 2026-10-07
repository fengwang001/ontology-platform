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

## 分代孤儿回收子系统（`orphan` 包）

跨链接类型的分代孤儿回收：链接类型的保留贡献只能配置为「独立保留」或「联合保留」；
孤儿先进第一代待回收队列，满第一代宽限期后转入第二代并重新计时，第二代宽限期满才
原子清理（清理时通过 `CascadeDeleter` 接入既有级联删除规则）。

```go
cfg := orphan.Config{
    Types: map[string]orphan.TypeConfig{
        "authoredBy": {Kind: orphan.Independent},
        "partOf":     {Kind: orphan.Joint, Requires: []string{"locatedIn"}},
        "locatedIn":  {Kind: orphan.Joint, Requires: []string{"partOf"}},
    },
    GraceGen1Ms: 24 * 60 * 60 * 1000, // 第一代宽限期
    GraceGen2Ms: 60 * 60 * 1000,      // 第二代宽限期（可更短）
}
sys, err := orphan.New(cfg, nil /* 墙钟 */, nil /* 默认级联，或注入自有实现 */)
sys.AddObject("obj-1")
_ = sys.AddEdge("authoredBy", "user-7", "obj-1")
sys.Scan() // 由定时器周期调用：第一代晋升、第二代清理
```

判定固定按「独立保留层 → 联合保留层」两层核对；队列脱离复用同一判定。每次判定与
代际推进都可经 `System.Logs()` / `orphan.Logger` 审计。设计取舍、被放弃方案与
逐条测试索引见 `orphan/DESIGN.md`。
