# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 授权决策缓存（`authz` 包）

`authz` 缓存（主体，动作，资源路径）的允许/拒绝判定结果，并在规则变更时
只失效真正受影响的条目，保证缓存命中的结果永远等于现算结果。

### 决策模型与依据节点

- 资源路径由斜杠分隔的段组成，根为 `/`；`/a` 是 `/a/b` 的祖先，但不是 `/ab` 的祖先。
- 规则为（节点路径，主体，动作，允许/拒绝），同节点同主体同动作至多一条，再设即覆盖。
- 判定（主体 s，动作 a，资源 r）：从 r 自身起沿祖先链 `r → parent(r) → … → /`
  找到第一条（s, a）规则，其所在节点称为**依据节点**；一条都没有则默认拒绝，
  依据记为「无」。缓存条目连同依据节点一并保存。

### 失效条件的推导

设变更只涉及节点 n 上的（s, a）规则。对任意资源 r，其判定只取决于 r 的祖先链
上（s, a）规则集合中离 r 最近的那条，因此：

- 不同主体或不同动作的条目：祖先链上（s, a）规则集合未变，判定不变，必保留。
- 同主体同动作的条目：变更前后分别现算，当且仅当（依据节点，结果）二元组
  发生变化时才失效。由此自然推出：
  - 在 n 的子树内、依据为 n 的真祖先或「无」的条目，依据改为 n（或删除 n 时
    退回更远的祖先），二元组改变，被失效；
  - 依据为 n 的更深后代的条目（最近规则不是 n），二元组不变，原样保留；
  - 兄弟子树、`/ab` 这类仅共享前缀字符串的路径，祖先链不含 n，不受影响；
  - 覆盖为相同取值时所有条目的二元组都不变，零失效（实现上直接不改动快照）。

失效条数只由本次变更与变更前已缓存的条目决定，与并发中的现算无关。

### 并发回填规则

- 规则存储为不可变快照（`atomic.Pointer`），设置/删除在写锁下整体换入新快照，
  随后在缓存锁下逐条复核同主体同动作的缓存条目并删除二元组变化者。
- `Decide` 未命中时：先读取当前快照并现算，**回填前再次比对快照指针**；
  若期间发生过规则变更，则丢弃旧结果并重试，绝不把已被变更影响的旧结果写入缓存。
- 因此任意交错下，每次返回的结果都等于调用期间某一时刻（所读快照生效时）的
  现算结果；所有调用静止后，失效复核已完成，每个缓存条目都等于现算结果。

### 本地验证

```bash
# 运行 authz 包全部测试（含竞态检测，日志打印输入、输出与判定依据）
go test -race -v ./authz/

# 关键用例
go test -race -v ./authz/ -run TestDeepSetInvalidation   # 深层新增规则的精准失效
go test -race -v ./authz/ -run TestStaleBackfillPrevented # 交错下旧结果不回填
go test -race -v ./authz/ -run TestConcurrentMixed        # 并发混合调用后一致性
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
