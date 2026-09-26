# Merkle 哈希树：推导与不变量

## 第三节推导（leafHash=fnv32(0x00‖data)，combine=fnv32(0x01‖l‖r)，4 字节大端）

| i | 数据 | leafHash | | i | 数据 | leafHash |
|---|------|----------|---|----|------|----------|
| 0 | "1" | 0x2076AF6A | | 4 | "5" | 0x1C76A91E |
| 1 | "2" | 0x1F76ADD7 | | 5 | "6" | 0x1B76A78B |
| 2 | "3" | 0x1E76AC44 | | 6 | "7" | 0x1A76A5F8 |
| 3 | "4" | 0x1D76AAB1 | | 7 | "8" | 0x1976A465 |

- 第 1 层：N10=combine(L0,L1)=0xA6ABC1F2，N11=combine(L2,L3)=0x2FEBAB2E，N12=combine(L4,L5)=0x588BA87E，N13=combine(L6,L7)=0x4F373FD5
- 第 2 层：N20=combine(N10,N11)=0xCD0D31A7，N21=combine(N12,N13)=0x097FB84D
- 根：root=combine(N20,N21)=0x0887B3C1

**(甲)** 叶子 "3"（i=2）的认证路径 = [L3=0x1D76AAB1, N10=0xA6ABC1F2, N21=0x097FB84D]（第 k 层兄弟下标 (i>>k)^1）。Verify(2,"3",路径)：k=0 偶→combine(L2,L3)=N11；k=1 奇→combine(N10,N11)=N20；k=2 偶→combine(N20,N21)=0x0887B3C1=根，**能还原**。

**(乙)** combine 参数写反：N10 错成 combine(L1,L0)=0xE0DDFA82（正确 0xA6ABC1F2）；逐层全反后根错成 0x8C22EE5E（正确 0x0887B3C1）。

**(丙)** Verify 左右判定写反：Verify(2,"3",正确路径) 在 k=1 错算 combine(N11,N10)，最终错根 0x23E1A70F（正确 0x0887B3C1）。

## 四条不变量（位置 / 钉住它的测试）

1. **路径可验证**：`tree.Proof` 按 `(i>>k)^1` 取兄弟、`tree.Verify` 按 `(index>>k)&1` 定左右；测试 `TestProofVerify`。
2. **与朴素参照一致**：`tree.Build` 逐层 `combine` 相邻两节点；测试 `TestRootMatchesNaive`（测试内用 fnv 原语独立重算）。
3. **防篡改**：`tree.Verify` 从 `leafHash(data)` 重算到底并比对根，不符即 false + `ErrRootMismatch`；测试 `TestTamperDetected`。
4. **失败不留痕**：`tree.Build` 先校验（空集/非 2 的幂）再建树，`Proof`/`Verify` 先验下标、只读不改树；测试 `TestRejectionKeepsState`（同测试还钉住四类哨兵错误互不相同）。
