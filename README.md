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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 共享页缓存 cgroup 记账迁移器（`pagecache` 包）

`pagecache` 包实现共享物理页按所有者记账：每个存活物理页的容量只记在一个所有者
cgroup 名下，多个 cgroup 可通过各自映射引用同一页。

- **引用 / 持有者 / 所有者**：`ref(c,id)` 为 cgroup c 的全部映射中页 id 的出现次数；
  `ref>0` 的 cgroup 为持有者；`since(c,id)` 为 ref 最近一次 0→正的 `now`，ref 保持为正
  时不变，归 0 后再升起重取当时 `now`。所有者取持有者中（since 升序、cgroup 名字节序
  升序）最小者；无持有者则页被回收并忘记大小。
- **记账**：`Used(c)` 只统计 c 拥有页的大小，`Logical(c)` 统计其持有不同页的大小，
  `Total()` 为全部存活页大小；任意时刻 `sum(Used) == Total()`。
- **Map 判定**：逐 distinct 页计算操作后所有者；操作后归本 cgroup 而操作前不是则计入
  `delta`（新页或 since 取等时因名字更小夺权）；`fresh` 为缓存中尚不存在的 distinct 页
  大小之和。先判 `Total+fresh > cap`（`ErrCapacity`，恰等通过），再判
  `delta>0 && used+delta > limit`（`ErrLimit`，恰等通过）。`delta==0` 的纯引用即使超额
  也放行。
- **Unmap 迁移**：逐页扣引用；持有者退出且原为所有者时迁给其余持有者中（since、名字）
  最小者，`used` 随之转移（允许接收者超额）；无人持有时回收，`Total` 下降。
- 错误次序、原子性与时钟规则见 `pagecache/doc.go`。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模型逐步对照）
go test ./pagecache -v

# 竞态检测（并发原子性）
go test -race ./pagecache

# 查看随机序列的输入 / 输出 / 判定依据日志
go test ./pagecache -run TestRandomAgainstNaiveModel -v

# 格式化与静态检查
gofmt -l .
go vet ./...
```

随机对照测试（`model_test.go`）内置一个只保留原始输入、每步全量重算 ref / since /
所有者 / used / total 的朴素模型；每一步后比对两侧决策结果与完整状态，并校验
`sum(used)==total`、每页恰有一个持有者所有者、引用数等于映射内出现次数等不变式。
