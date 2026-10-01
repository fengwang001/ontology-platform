# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 哈希序目录读游标

根包提供 `HashDirectory`，用名字哈希和同哈希序号生成稳定的 64 位目录键。

- 哈希：`h` 初值为 `2166136261 ^ seed`，对名字的每个字节依次执行 `h = (h ^ byte) * 16777619`，所有运算以 32 位无符号整数回绕。
- minor：每个 `h` 桶中的存活项占用从 0 开始的序号；新增或改名插入时取当前最小空闲值，删除或改名离开该桶后立即释放。
- 排序键：`key = uint64(h)<<32 | uint64(minor)`，目录始终按 `key` 升序读取。
- 容量：构造参数 `M` 的合法范围是 `1` 到 `2^31-1`；同一哈希桶已有 `M` 项时，插入、会导致超限的改名和重哈希都会返回 `ErrHashFull`。

`Cookie{Gen, Pos}` 是分页游标：`Pos=0` 表示起点且不校验代数；否则当前代游标直接使用，上一代游标只能按快照迁移。`ReadDir(c, n)` 返回所有 `key >= c.Pos` 项中升序的前 `n` 项。成功返回至少一项时，新游标为当前代、`Pos=最后一项 key+1`；未返回项时保留传入或迁移后的游标。`Done` 表示不存在任何 `key >= 新游标 Pos` 的存活项。`n=0` 合法，也会完成游标代数归一化或迁移并计算 `Done`。

`Rename(old, new)` 保持 inode 不变，并严格按“先删除 old，再插入 new”处理容量；新旧名字同哈希时，old 刚释放的 minor 可被 new 复用。

`Rehash(newSeed)` 总会把代数加 1，即使种子相同。提交前先按名字字节序模拟全部重新插入：某个哈希桶超过 `M` 时整体拒绝，代数、快照、种子和目录项都不变。提交时保存唯一一份上一代“名字到旧 key”快照，然后所有项按名字字节序重新获得当前最小空闲 minor。上一代游标满足以下条件才可迁移：

- `Cookie.Gen == 当前代数 - 1` 且 `Pos != 0`；
- 上一代快照中存在名字 `q`，其旧 key 恰好等于 `Pos-1`；
- `q` 在当前代仍存在。

满足时游标迁移为 `(当前代数, q 的当前 key+1)`；否则返回 `ErrCursorExpired`。连续两次重哈希后，第一代游标不能再迁移。`Pos=0` 的游标永远可用并归一化为当前代起点。

可通过 `errors.Is` 区分 `ErrInvalidArgument`、`ErrNotFound`、`ErrExists`、`ErrHashFull` 和 `ErrCursorExpired`。所有公开方法都在同一把互斥锁下串行化状态变化，因此并发调用等价于某种合法串行顺序。

## 本地验证

```bash
go test ./...

# 2000 组随机操作与朴素模型对照，日志包含输入、输出和判定依据
go test -run TestRandomModelComparison -v

# 竞态检测
go test -race ./...
gofmt -l .
go vet ./...
```
