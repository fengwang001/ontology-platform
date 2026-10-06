# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 协作文档同步（`collab` 包）

`collab` 实现了支持离线编辑的协作文档同步服务端：覆盖型（Set，带字段
版本仲裁）与累加型（Add，并发可交换）字段、事务组（全有或全无、组依赖
失败链）、最近 1000 个操作结果的 O(1) 重放、批级拒绝优先级（参数非法 >
时钟回退 > 重放过期 > 序号缺口）与确定性重放。

```go
srv := collab.NewServer()
srv.Create("doc1", map[string]collab.Kind{"title": collab.KindSet, "n": collab.KindAdd})
res, err := srv.Sync("clientA", "doc1", []collab.Op{
    {Seq: 1, Type: collab.OpSet, Field: "title", Value: 7, BaseVer: 0, Group: "g1"},
    {Seq: 2, Type: collab.OpAdd, Field: "n", Delta: 5, Group: "g1"},
}, 10)
snap, _ := srv.Get("doc1")        // 全部字段的值/版本 + 文档修订号
srv.Pending("clientA", "doc1")    // 该客户端已处理最大序号
```

结果类别：`ResApplied` 已应用、`ResMerged` 已合并、`ResConflict` 冲突、
`ResAhead` 基线超前、`ResKindMismatch` 种类不符、`ResOverflow` 溢出、
`ResDepFailed` 依赖失败、`ResGroupAborted` 组内连带失败。

设计取舍、被放弃方案与复杂度证明见 `collab/DESIGN.md`。

```bash
go test ./collab -race -count=1                       # 全量 + 竞态
go test ./collab -run TestRandomDifferential -v       # 2000 组随机差分
go test ./collab -run xxx -bench .                    # 性能边界
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
