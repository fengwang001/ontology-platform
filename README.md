# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 复合类型布局子系统

根包提供 `NewRegistry`、`Register`、`Modify`、`Layout`、`View`、`Views`。

1. `config.go` 与 `types.go`：配置、字段规格、布局、视图和错误类型。
2. `layout.go`：无状态偏移、大小、对齐和紧凑字段计算。
3. `compatibility.go`：字段身份优先的完全/追加/不兼容判定。
4. `registry.go`：RWMutex 串行化写操作，查询返回深拷贝快照。
5. `dependencies.go`：仅沿直接内嵌反向边拓扑调度；间接引用不入依赖图。

关键取舍：候选布局全部算完后才提交，修改依赖者超限会整体回滚；只有实际布局身份改变才递增版本。放弃全局布局缓存和查询时重算，因为前者会让兼容性来源不明确，后者会随类型总数增长。放弃把间接引用纳入传播，因为其大小和对齐由配置固定。直接依赖图支持环检测；间接引用允许前向目标和合法环。

验证：`go test -race ./...`；`go test -run TestRandomSequenceAgainstNaiveModel -v` 对固定种子随机序列与朴素全量重算模型比较；benchmark 分别构造 100 和 10000 个无关类型，单类型布局约 996ns/1043ns，局部修改约 3701ns/3866ns，结果不随无关类型数量增长。

日志：向 `Config.Logger` 注入实现即可记录每条输入、实际输出和判定依据。

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
