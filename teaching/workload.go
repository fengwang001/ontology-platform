package teaching

import "math/big"

// scaleCoeff 返回规模档位系数（整数百分数）。边界取等归高档。
// 非法班级规模（无档位覆盖）返回 false。
func scaleCoeff(tiers []ScaleTier, classSize int) (int, bool) {
	for i := range tiers {
		if classSize <= tiers[i].MaxSize {
			if i == 0 || classSize > tiers[i-1].MaxSize {
				return tiers[i].Coeff, true
			}
		}
	}
	return 0, false
}

// exactWorkload 一次性完成 学时 × 规模系数 × (新开课加成) × 实验课系数 的精确
// 计算，再整体向下取整到整数。系数以整数百分数给出（分母 100 或 10000），
// 使用 math/big 保证任意输入下结果可精确复现，严禁分步取整。
// newCourseCoeff = 100 + NewCourseAdd（百分数）；labCoeff 为 100 或 LabCoeff。
func exactWorkload(hours, scaleC, newCourseC, labC int) int {
	num := new(big.Int).SetInt64(int64(hours))
	num.Mul(num, big.NewInt(int64(scaleC)))
	num.Mul(num, big.NewInt(int64(newCourseC)))
	num.Mul(num, big.NewInt(int64(labC)))
	return int(new(big.Int).Quo(num, big.NewInt(100*100*100)).Int64())
}

// taskCoeffs 返回任务命中的三个整数百分数系数。
func taskCoeffs(cfg Config, spec *TaskSpec) (scaleC, newCourseC, labC int, ok bool) {
	scaleC, ok = scaleCoeff(cfg.Tiers, spec.ClassSize)
	if !ok {
		return 0, 0, 0, false
	}
	newCourseC = 100
	if spec.NewCourse {
		newCourseC = 100 + cfg.NewCourseAdd
	}
	labC = 100
	if spec.IsLab {
		labC = cfg.LabCoeff
	}
	return scaleC, newCourseC, labC, true
}
