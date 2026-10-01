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

## 直接模式清单缓存

`Cache` 按非空动作键保存多份编译结果缓存条目。每份条目包含实际读取过的文件清单、结果摘要与逻辑时间 `last`。

- `New(perKeyLimit, globalLimit)`：分别指定每键上限 `M` 与全局上限 `Cap`；两者都必须至少为 1，且先检查 `M`，再检查 `Cap`。
- `Put(key, manifest, result)`：清单必须是非空的 `ReadItem` 序列，路径按字节序严格递增，每个内容摘要与结果摘要均为非空字符串。
- 命中判定：`Lookup` 只考察指定键下的条目。条目中的每个路径都必须存在于当前文件映射中，且内容摘要逐字节相等；映射中缺少路径表示文件不存在，现状中的额外文件不影响命中。
- 多命中选择：多个条目同时命中时不比较清单长短，也不比较子集关系，只选择 `last` 最大的条目。
- `tick` 从 0 开始；成功 `Put`（包括同清单覆盖结果）与命中的 `Lookup` 各将 `tick` 加 1，并把相关条目的 `last` 置为新值。因此所有 `last` 全局互不相同。
- 未命中、`Dump("")`、`Dump` 未知键都不是错误；未命中不推进 `tick`。
- 新条目加入后，先在该键内反复淘汰 `last` 最小者直到条目数不超过 `M`，再在全局范围反复淘汰 `last` 最小者直到总数不超过 `Cap`。
- 相同清单（路径和摘要逐对完全相同）重复 `Put` 时只覆盖结果并刷新 `last`，不新增条目。
- `Dump(key)` 返回该键全部条目的副本，按 `last` 降序排列；`Len()` 返回全局条目总数。

### 错误优先级

被拒绝的操作不会修改 `tick`、条目或淘汰状态。`Put` 只返回按以下顺序遇到的第一个错误：

1. 键为空
2. 清单为空
3. 存在空路径（先检查全部路径非空，再检查递增性）
4. 路径未严格递增（相等或回落）
5. 存在空内容摘要
6. 结果摘要为空

`Lookup` 的键为空会返回错误。`Dump` 的空键或未知键正常返回空列表。

### 并发与确定性

所有缓存操作由同一个互斥保护，并发调用的结果等价于某个合法串行顺序。由于 `tick` 和 `last` 只由成功写入或命中更新，重放相同操作序列会得到相同命中结果、淘汰结果与逻辑时间。

查找直接定位到对应键的条目桶，不扫描其他键。测试使用非导出计数器，在 100 个键和 10000 个键、每键存满 `M=4` 个条目时，验证单次 `Lookup` 考察的条目数均不超过 4。

### 本地验证

```bash
# 普通全量测试
go test ./...

# 打印 2000 组随机操作序列对拍的输入、输出与判定依据
go test -run TestRandomLinearOracle -v ./...

# 竞态检测，包括 100/10000 键查找上界测试
go test -race ./...

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
