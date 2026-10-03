# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 推送设备令牌注册表

`pushregistry.Registry` 维护“用户 / 设备 / 当前令牌 / 宽限期旧令牌”的精确归属，并通过互斥锁保证所有操作可并发调用且结果等价于某种串行顺序。

构造参数：

- `D`：每用户设备上限，范围 `1..64`。
- `R`：每个绑定保留的旧令牌数，范围 `0..8`；`0` 表示轮换后立即释放旧令牌。
- `G`：旧令牌下发宽限期，范围 `0..1_000_000_000`。
- `B`：服务商反馈失效后的拒绝时长，范围 `0..1_000_000_000`。

### 注册、释放与淘汰

`Register(u, d, tok, now)` 在参数合法、时钟未回退且 `blk[tok] <= now` 后按以下顺序执行：

1. 若 `tok` 已经是 `(u,d)` 的当前令牌，只刷新 `lastSeen`。
2. 若 `tok` 是其他绑定的当前令牌，先删除整个来源绑定及其旧令牌。
3. 若 `tok` 是任意绑定的旧令牌，只从来源旧令牌列表移除。
4. 目标绑定已存在时，原当前令牌以 `(原令牌, now)` 放到旧令牌列表头部，超过 `R` 的最旧项立即释放，然后写入新当前令牌。
5. 目标是新设备时，先计入步骤 2/3 已造成的删除，再判断设备数；达到 `D` 时淘汰 `lastSeen` 最小者，并列淘汰设备名字节序较小者，最后插入新绑定。

因此，当前令牌转给同一用户的另一台设备会先减少设备数，再插入目标设备，不会额外淘汰第三台设备。

### 查询、宽限与反馈

`Targets(u, now)` 是只读操作：

- 当前令牌按 `lastSeen` 降序、设备名字节序升序返回。
- 旧令牌按退役时刻降序、令牌字节序升序返回。
- 旧令牌仅在 `now < retiredAt + G` 时返回；`now == retiredAt + G` 已过期。
- 过期旧令牌不会自动删除，仍占用 `R` 个名额并保持全局归属，仍可被其他设备或用户注册夺走。

`Feedback(tok, now)` 要求令牌当前有归属：当前令牌会删除整个绑定，旧令牌只移除自身；随后设置 `blk[tok] = now + B`。`Register` 在 `now < blk[tok]` 时拒绝，恰好在 `blk[tok]` 时放行；`B=0` 表示反馈后可立即重新注册。`Unregister` 只释放令牌，不写拒绝表。

错误按固定优先级返回第一个原因：参数非法、时钟回退，随后注册为令牌拒绝期，`Touch`/`Unregister` 为设备不存在，`Feedback` 为令牌无归属。被拒绝操作不会更新绑定、归属、拒绝表或最大已接受时刻。

注册表按用户维护设备索引，并用非导出计数器累计每次 `Targets` 实际考察的“当前绑定数 + 旧令牌数”，测试可验证查询成本只随目标用户的数据增长。

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

# 只验证推送注册表（若 Go 不在 PATH）
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -race -v ./pushregistry

# 朴素模型 2000 组随机对拍；失败时 testing 会保留输入、输出与判定依据日志
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -v ./pushregistry -run TestRandomSimulation2000

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
