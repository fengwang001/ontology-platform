# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 别名链解析缓存（`resolver` 包）

`resolver.Cache` 沿别名（CNAME 式）链把名字解析到地址或“不存在”终点，
并对链上每一环做正/负缓存；只有缺失的环才向上游补查。

### 上游应答

上游以注入函数 `Upstream func(name) (Answer, error)` 给出，三类应答：

- `AliasAnswer(target, ttl)`：别名记录，指向下一个名字；
- `AddressAnswer(addresses, ttl)`：地址记录，链的成功终点；
- `NXDOMAINAnswer(soaTTL, minimumTTL)`：不存在应答，否定终点，
  其缓存存活取 `min(soaTTL, minimumTTL)`。

上游返回错误即“上游失败”。

### 链解析规则

- 从请求名字开始逐环处理：先查缓存，未命中才向上游查询；因此别名环已缓存、
  仅终点地址环失效时，只会补查地址环。
- 别名记录超过 `MaxAliasRecords`（8）条即为链过长；第 9 个名字若直接是
  地址/否定终点仍允许，若是又一条别名则拒绝。
- 链上任何名字（含缓存与上游来源）再次出现即为成环；自环也算成环。
- 拒绝原因按固定顺序判定，不会只按发现顺序报第一个：
  名字为空 → 链成环 → 链过长 → 上游失败。
- 成功时返回整条别名链、地址集与存活时间；否定终点返回 `Found=false`。
- 返回的存活时间 = 链上所有环（含终点）剩余时间的最小值：
  缓存环取“到期时刻 − 现在”，上游新得环取其应答 TTL。

### 缓存与淘汰

- 缓存按名字保存一条记录，同名写入为替换；到期时刻 = 存入时刻 + TTL，
  现在不早于到期时刻（`now >= expireAt`）即失效，恰在到期时刻也算失效。
- TTL 为 0 的记录不写入缓存，但仍参与当次解析（可使返回 TTL 归零）。
- 不存在应答以否定条目保存，TTL = `min(S, M)`。
- 容量为 C 条；每次存入新名字前先清除全部失效条目，仍然满时淘汰到期最早者，
  到期时刻并列时淘汰名字字典序最小者。条目数永不超过 C。
- 失败的解析不污染缓存：成环、链过长或上游失败时，本次已从上游取得的
  全部记录都不写入，缓存保持调用前状态（也不触发失效清除/淘汰）。

### 并发语义

- `Resolve` 可被并发调用。同一根名字的并发解析合并为一次：等待者直接得到
  同一份结果；上游失败时所有等待者得到同一错误。
- 链中同一名字的并发上游查询同样合并为一次（不同根解析共享同一缺失环）。
- 给定相同调用序列与相同上游应答序列，重放结果（含 TTL 与链）完全相同；
  时间通过 `Config.Clock` 注入，便于确定性测试。
- 注入 `Config.Logger` 后，每次解析都会打印输入、输出以及每一环的判定依据
  （缓存命中/未命中、剩余时间、最小 TTL、成环/过长/失败原因、提交条目数）。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志）
go test -race -v ./resolver

# 重复运行以压测并发合并
go test -race -count=10 ./resolver

# 全模块检查
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖：返回 TTL 取链上最小剩余、别名命中而地址环失效只补查地址环、
否定条目取 `min(S,M)`、TTL=0 不缓存、恰在到期时刻失效、成环/链过长/
上游失败后缓存不变、空名最先拒绝、并发单次上游查询与共享失败、容量淘汰
（最早到期 + 字典序并列）及相同序列确定性重放。

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
