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

## RBAC SSD/DSD 语义

管理器维护角色、用户、权限、会话、角色继承 DAG、用户角色分配和角色权限分配。所有名称均为非空字符串（对应非空字节串），并按字节序进行确定性排序。

- `Juniors(r)`：角色 `r` 本身，加上沿继承边 `(senior, junior)` 可到达的全部 junior。
- `Auth(u)`：用户 `u` 被直接分配的每个角色的 `Juniors` 之并。
- `A(s)`：会话 `s` 的显式激活角色集合；其中每个角色都必须属于所属用户的 `Auth(u)`。
- `Eff(s)`：`A(s)` 中每个角色的 `Juniors` 之并。
- `Check(s,p)` 为真，当且仅当权限 `p` 被分配给 `Eff(s)` 中至少一个角色。

约束判据如下：

- SSD `(名称, RS, n)`：对任一用户 `u`，当 `|Auth(u) ∩ RS| >= n` 时违反。
- DSD `(名称, RS, n)`：对任一会话 `s`，当 `|Eff(s) ∩ RS| >= n` 时违反。
- 因此交集个数为 `n-1` 时通过，为 `n` 时拒绝。
- `RS` 至少包含两个互不相同的现有角色，且 `2 <= n <= |RS|`。

## 检查对象与原子性

- `AddInherit(senior, junior)`：先拒绝重复边和成环；SSD 只检查“直接分配角色的 Juniors 含 `senior`”的去重用户，候选 `Auth` 为原集合并上 `Juniors(junior)`。SSD 全通过后，DSD 只检查“显式激活角色的 Juniors 含 `senior`”的去重会话，候选 `Eff` 同理扩展。
- `AssignUser`：拒绝重复分配后，只以该用户的候选 `Auth` 检查全部 SSD。
- `AddSSD`：登记前以现有全部用户检查新约束；`AddDSD` 则以现有全部会话的 `Eff` 检查新约束。
- `Activate`：要求角色已在用户 `Auth` 中且尚未显式激活，然后用 `A(s) ∪ {role}` 推导出的候选 `Eff` 检查全部 DSD。
- 任一约束违反即整体拒绝；约束名按字节序最小优先，其下用户或会话名按字节序最小优先；同一次操作中 SSD 报告先于 DSD。
- 拒绝顺序固定为参数非法、不存在、冲突、约束违反、超限；被拒绝操作在任何检查失败前都不会修改状态。
- 所有检查与后续提交在同一互斥区完成，并发调用等价于某个串行顺序；`Check` 使用读锁观察一致快照。

## 级联停用

`DeassignUser(u,r)` 和 `DeleteInherit(senior,junior)` 先确认分配或边存在，生效后重算受影响用户的新 `Auth`，再停用这些用户会话中不再属于新 `Auth` 的显式激活角色。角色若仍可通过其他继承路径到达，则不会停用。

`DeleteInherit` 的受影响用户为删除前“直接分配角色的 Juniors 含 `senior`”的去重用户；停用判定覆盖这些用户的全部会话，但仅移除不合法的显式角色。返回列表按会话名、再按角色名字节序升序。删除只会缩小可达集合，因此停用后不会产生新的 DSD 违反。

`Deactivate(s,r)` 只移除显式激活角色：

- `r` 在 `A(s)` 中：移除并立即重算 `Eff(s)`。
- `r` 不在 `A(s)` 但在 `Eff(s)` 中：报冲突“隐式激活”，并列出所有能蕴含 `r` 的显式角色，升序返回。
- `r` 不在 `Eff(s)` 中：报冲突“未激活”。

内部按角色维护用户和显式激活会话反查索引。非导出计数只记录 `AddInherit` 与 `DeleteInherit` 的受影响用户/会话数量；添加无关的 10^4 个用户与会话不会改变同一调用的计数。`AddInherit` 在 SSD 阶段失败时不会进入 DSD，会话计数为 0。

## 本地验证

```bash
# 常规测试（包含 2000 组随机序列与朴素全量模拟差分）
go test ./...

# 查看随机序列的输入、输出、判定依据
go test -v -run TestRandomDifferential2000

# 并发数据竞争检测
go test -race ./...

# 静态检查
go vet ./...
```
