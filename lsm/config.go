package lsm

import (
	"fmt"
	"math/big"
)

// Config 描述分层结构的静态参数。层号从零起，共 NumLevels 层，
// 最后一层不参与压实选择。
type Config struct {
	// NumLevels 总层数，至少为 2。
	NumLevels int
	// L0Trigger 零层触发阈值：零层分数 = 文件数 / L0Trigger。
	L0Trigger int64
	// BaseLevelBytes 第一个非零层（第 1 层）的目标字节数。
	BaseLevelBytes int64
	// LevelMultiplier 相邻层目标字节数的固定倍数，至少为 1。
	LevelMultiplier int64
}

// validate 校验配置合法性。
func (c Config) validate() error {
	if c.NumLevels < 2 {
		return fmt.Errorf("%w: NumLevels must be >= 2, got %d", ErrInvalidArgument, c.NumLevels)
	}
	if c.L0Trigger < 1 {
		return fmt.Errorf("%w: L0Trigger must be >= 1, got %d", ErrInvalidArgument, c.L0Trigger)
	}
	if c.BaseLevelBytes < 1 {
		return fmt.Errorf("%w: BaseLevelBytes must be >= 1, got %d", ErrInvalidArgument, c.BaseLevelBytes)
	}
	if c.LevelMultiplier < 1 {
		return fmt.Errorf("%w: LevelMultiplier must be >= 1, got %d", ErrInvalidArgument, c.LevelMultiplier)
	}
	return nil
}

// levelTargetBytes 返回第 level 层（level >= 1）的目标字节数：
// BaseLevelBytes * LevelMultiplier^(level-1)。用 big.Int 计算以避免溢出。
func (c Config) levelTargetBytes(level int) *big.Int {
	target := big.NewInt(c.BaseLevelBytes)
	mult := big.NewInt(c.LevelMultiplier)
	exp := big.NewInt(int64(level - 1))
	return target.Mul(target, new(big.Int).Exp(mult, exp, nil))
}
