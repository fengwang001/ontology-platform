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

当前交付聚焦可复用的 Go 包 `ontology/`，尚无 `cmd/server` HTTP 入口；可在测试或其他 Go 服务中直接调用 `ontology.NewGateway()`。

## 核心能力

- 类型版本演进：新增属性、收紧约束、lineage 保持的弹性重命名、废弃标识符。
- 属性级授权与吊销：权限绑定稳定 `LineageID`，在重命名后自然延续。
- 历史审计：废弃属性的授权记录保留，但不参与线上判定。
- 写策略：类型级全局选择 `reject_object` 或 `ignore_field`，拒绝混用。
- 读投影：不返回零值或掩码，不重新触发投影后的 required schema 错误。
- 线性化并发：进程内全局临界区保证可观察结果等价于某个串行顺序。
- 判定日志：记录每次操作的输入、输出、依据和错误类别。
- 规模无关视图：`PermissionView` 只索引指定版本快照和一个版本记录，不扫描历史权限事件。

错误通过 `ontology.Error.Kind` 区分，主要类别包括 `object_not_found`、`version_stale`、`type_permission_denied`、`property_identifier_not_found`、`permission_revoked`、`attribute_permission_denied` 和 `write_policy_conflict`。

更多取舍和性能证明见 `docs/design.md`。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 只运行随机朴素模型对照
GOCACHE=/tmp/ontology-go-cache go test -run TestRandomOperationSequenceMatchesNaiveModel -v ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

如果系统默认 `GOCACHE` 位于只读目录，可显式设置：

```bash
export GOCACHE=/tmp/ontology-go-cache
```
