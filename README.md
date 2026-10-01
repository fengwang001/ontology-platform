# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 节点准入与驱逐管理器

`Manager` 登记节点、污点和已绑定 Pod，内部以互斥锁串行化所有并发调用。

### 容忍匹配

- `Equal`：效果、键相同且值相等时匹配；`Exists`：效果、键相同即匹配且值必须为空串。
- 容忍的效果为空串时可匹配任意效果；键为空串时必须使用 `Exists`，可匹配任意键，但非空效果仍需相等。
- 调度时，`NoSchedule` 与 `NoExecute` 污点都必须至少被一个容忍匹配；`PreferNoSchedule` 不拦截调度。
- 非法容忍包括未知运算符、`Exists` 带值、空键配合 `Equal`、非精确 `NoExecute` 容忍携带非 `-1` 秒数，以及 `NoExecute` 秒数超出允许范围。

### 驱逐时刻推导

- 每个 `NoExecute` 污点按 Pod 容忍的声明顺序取第一个匹配项。
- 无匹配容忍时，到期时刻为污点的 `addedAt`；秒数为 `-1` 时永不到期；否则到期时刻为 `addedAt + seconds*1000`。
- Pod 的驱逐时刻为当前节点所有 `NoExecute` 污点到期时刻的最小值；没有有限到期值时永不驱逐。
- 时间始终从污点添加时刻起算并由当前污点集合实时推导，不存储逐 Pod 计时器。
- `Taint` 替换已存在 `(键, 效果)` 时只更新值并保留 `addedAt`；`Untaint` 后重新添加会生成新的 `addedAt`。

### 限速与终止占位

- 每次 `Tick(now)` 内，每个节点先按“驱逐时刻升序、Pod ID 字节序”选择运行中且 `deadline <= now` 的 Pod，最多驱逐 `R` 个。
- 全局返回列表同样按“驱逐时刻升序、Pod ID 字节序”排序。
- 被驱逐 Pod 进入终止中，释放时刻为 `now + G`；在 `releaseAt <= now` 前继续占用节点容量且同 ID 不可重绑，之后才被删除。
- `G=0` 时驱逐与释放发生在同一时刻；终止中的 Pod 不会再次参与驱逐。

### 拒绝顺序

构造参数非法整体返回 `invalid_config`。已创建管理器的操作按以下顺序报告第一个错误：

1. `invalid_argument`：空节点名、空 Pod ID、空污点键、非法效果、非法容忍或非法 `now`。
2. `clock_backtrack`：`now` 小于此前被接受的 `Taint`、`Untaint`、`Schedule` 或 `Tick` 已见最大时刻。
3. `Schedule` 的 Pod ID 仍在运行或终止占位中。
4. 节点不存在。
5. `Schedule` 遇到不可容忍污点，错误携带按“键字节序、效果字节序”选出的首个 `(键, 效果)`。
6. 节点容量不足。

`AddNode` 重名返回 `node_exists`，`Untaint` 目标污点不存在返回 `taint_not_found`；被拒绝的操作不会改变任何状态。

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

# 若默认 Go 构建缓存目录不可写，可指定临时缓存
GOCACHE=/tmp/ontology-go-cache go test -race -v ./...

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
