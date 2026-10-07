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

## 分块快照导出器（`snapshot` 包）

本体快照按对象类型分块落盘，每块自带声明条数与 sha256 校验和；加载侧做
无状态只读完整性校验、数量核对、跨块引用保守判定，并在满足条件时生成跨类型
聚合视图。

```go
import "ontology/snapshot"

// 导出（按类型分块）
snapshot.Export(dir, snapshot.ExportRequest{Chunks: map[string][][]Record{
    "Person":  {{ {ID: "p1", Refs: []snapshot.CrossTypeRef{
        {Field: "worksFor", TargetType: "Company", TargetID: "c1"}}} }},
    "Company": {{ {ID: "c1"} }},
}}, logger)

loader := snapshot.NewLoader(dir)
res, _ := loader.Load([]string{"Person", "Company"}, logger)   // 单块可取
agg, _ := loader.Aggregate([]string{"Person", "Company"}, logger)
```

错误类别互斥可区分：范围外请求、块完整性失败、数量不一致、悬空引用、
因目标块不可信而无法校验；拒绝优先级与判定依据见 `snapshot/DESIGN.md`。

```bash
go test -race -v ./snapshot                                    # 判定日志逐条可见
go test -run TestRandomDifferential -v ./snapshot             # 独立朴素模型随机对照
go test -run TestReferenceLookupScaleIndependent -v ./snapshot # O(1) 规模证据
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
