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

## 层级资源授权缓存

`ontology.NewDecisionCache(ruleLimit)` 用 `(subject, action, resourcePath)` 作为缓存键，值中同时保存允许/拒绝结果和依据节点。

- 路径只接受根 `/` 或以 `/` 开头的非空段序列；祖先判断按整段比较，所以 `/a` 是 `/a/b` 的祖先，不是 `/ab` 的祖先。
- 决策从资源自身开始沿祖先链向上查找，最近一条同主体、同动作规则即依据节点；整条链没有规则时返回拒绝，依据记为“无”。
- 规则写入覆盖同一 `(节点路径, 主体, 动作)` 的旧规则；新增规则还要检查规则上限，覆盖不受上限影响。
- 设置或删除规则时，只扫描变更节点子树内同主体、同动作的缓存条目。对每个条目比较变更前缓存的 `(依据节点, 结果)` 与变更后现算的二元组，不同才删除并计入失效数。
- 更深节点仍为依据的条目、兄弟子树、`/ab` 这类前缀相似但不是后代的路径，以及同值覆盖产生的条目，二元组不变，均原样保留。

### 并发回填

决策读取和规则变更共用单调递增的规则版本。未命中先在读锁下记录版本并现算；取得写锁准备回填时，如果期间发生过规则变更，丢弃旧结果并在当前规则快照下重新计算。这样并发现算与设置、删除交错时不会把旧依据或旧结果写入缓存。每次调用返回的结果都是其执行期间某个有效规则快照下的现算结果；所有调用静止后，保留下来的缓存条目均等于当前现算结果。

统计项由 `Stats()` 返回：

- `Hits`：直接返回已有缓存条目的次数。
- `Computations`：实际需要沿祖先链查找规则的次数。
- `Invalidated`：规则变更后二元组改变并删除的缓存条目数，只由已缓存条目决定。

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

# 仅验证授权缓存
go test -race -v ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
