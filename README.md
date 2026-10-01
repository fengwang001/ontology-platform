# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 保存点恢复准入器（`savepoint` 包）

`savepoint.Admitter` 在新作业图与保存点之间按算子标识与来源映射匹配状态、
判定兼容性，保证恢复要么完整可用、要么零副作用地被拒绝。

### 承接规则

- 保存点登记（`RegisterSavepoint`）后不可变，含各算子的标识、最大并行度 `m`
  与若干具名状态项（种类为 value/list/map，值类型为 int/long/string）。
- 新算子声明了来源标识（`SourceID`）则承接保存点中该标识的算子；
  否则承接与自身同名的保存点算子；两者都没有则为新增算子，以空状态启动，
  最大并行度缺省 128。
- 承接者的最大并行度缺省继承保存点的 `m`，且必须等于保存点的 `m`。
- 未被任何新算子承接的保存点算子、被承接算子中新图缺失的同名状态项，
  仅在允许丢弃（`allowDiscard=true`）时可恢复，并记入计划的丢弃清单；
  新图新增的状态项以空状态启动。
- 同名状态项种类须相同；值类型相同或由 int 变宽为 long 才兼容。
- 成功时返回每个算子的动作：`restore-direct`（直接恢复）、
  `restore-widen`（变宽迁移后恢复）、`start-empty`（空启动）。

### 拒绝次序

拒绝原因按以下次序只报第一类，同类对象全部升序列出：

1. `savepoint-not-found`：保存点不存在
2. `job-id-in-use`：作业标识已被占用
3. `invalid-graph`：标识为空或重复、`p` 非正或大于最大并行度、来源不在保存点内
4. `duplicate-claim`：同一保存点算子被多个新算子承接
5. `max-parallelism-mismatch`：承接者最大并行度与保存点 `m` 不一致
6. `unclaimed-operators`：未允许丢弃的未承接算子
7. `incompatible-state-items`：不兼容的状态项
8. `missing-state-items`：未允许丢弃的缺失状态项

恢复成功后作业标识即被占用，直到 `Stop`；保存点被存活作业引用时
`DeleteSavepoint` 拒绝，作业停止后才可删。所有方法可并发调用：
同一作业标识并发恢复只成功一个，被拒绝的操作不改变任何状态，
相同输入得到完全相同的计划与清单。

### 本地验证

```bash
# 全部测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./savepoint/
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
