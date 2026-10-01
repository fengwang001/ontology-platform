# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 直接模式清单缓存（`./cache`）

编译结果的直接模式清单缓存：为同一动作键保存多份
「实际读取文件摘要清单 -> 结果摘要」条目，使命中只取决于动作
真正读取过的文件，且淘汰次序可精确复现。

### 命中判定

- 条目清单是（路径，内容摘要）对的序列，路径按字节序严格递增。
- `Lookup(键, 现状)` 中，现状是路径到当前内容摘要的映射，映射里
  没有的路径表示文件不存在。
- 条目命中当且仅当其清单中每一对的路径都在现状中且摘要逐字节
  相等；清单之外的文件不影响命中。
- 多个条目同时命中时取 `last` 最大者，不比较清单长短，也不管
  是否互为子集；未命中是正常结果（返回 `hit=false`），不是错误。

### tick 与淘汰规则

- 逻辑序号 `tick` 从 0 起，每次成功的 `Put` 与每次命中的
  `Lookup` 各使 `tick` 加 1，并把涉及条目的 `last` 置为新
  `tick`，因此 `last` 全局互不相同；未命中与被拒绝的操作不改
  `tick`。
- `Put` 时若该键下已有清单逐对完全相同的条目，只覆盖结果并刷新
  `last`，条目数不变；否则新增条目，随后先在该键内反复淘汰
  `last` 最小者直到该键条目数不超过 M，再在全局范围反复淘汰
  `last` 最小者直到总条目数不超过 Cap（键内淘汰先于全局淘汰）。
- `Dump(键)` 返回该键全部条目按 `last` 降序（键为空或不存在时
  返回空列表，不是错误）；`Len` 返回全局条目总数。
- 相同操作序列重放得到完全相同的命中结果、淘汰序列与 `tick`。

### 错误优先级

- 构造：`M < 1` 先于 `Cap < 1` 报（`ErrInvalidM`、
  `ErrInvalidCap`）。
- `Put` 按以下顺序只报第一个：键为空（`ErrEmptyKey`）、清单为空
  （`ErrEmptyManifest`）、空路径（`ErrEmptyPath`，先于递增性
  检查）、路径未严格递增（`ErrPathsNotOrdered`）、空摘要
  （`ErrEmptyDigest`）、结果为空（`ErrEmptyResult`）。
- `Lookup` 的键为空报 `ErrEmptyKey`；被拒绝的操作整体生效失败，
  不改变 `tick` 与任何条目。

### 本地验证

```bash
# 全部缓存测试（含 2000 组随机序列与线性表朴素实现对拍，
# 详细日志打印每条操作的输入、输出与判定依据）
go test ./cache -v

# 竞态检测
go test ./cache -race

# 单次 Lookup 考察条目数不随键总数增长（100 键与 10000 键均 <= M）
go test ./cache -run TestLookupExaminedBound -v
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
