package medsched

// Allergies 保存每个患者的过敏登记（药品或类别）。
type Allergies struct {
	drugs      map[string]map[string]bool // patient -> set(drugID)
	categories map[string]map[string]bool // patient -> set(category)
}

// NewAllergies 创建空过敏登记。
func NewAllergies() *Allergies {
	return &Allergies{
		drugs:      map[string]map[string]bool{},
		categories: map[string]map[string]bool{},
	}
}

// AddDrug 登记患者对某药品过敏。
func (a *Allergies) AddDrug(patient, drug string) error {
	if patient == "" || drug == "" {
		return errf(ErrInvalidParam, "过敏登记参数非法")
	}
	if a.drugs[patient] == nil {
		a.drugs[patient] = map[string]bool{}
	}
	a.drugs[patient][drug] = true
	return nil
}

// AddCategory 登记患者对某类别过敏。
func (a *Allergies) AddCategory(patient, category string) error {
	if patient == "" || category == "" {
		return errf(ErrInvalidParam, "过敏登记参数非法")
	}
	if a.categories[patient] == nil {
		a.categories[patient] = map[string]bool{}
	}
	a.categories[patient][category] = true
	return nil
}

// HasAllergy 判断患者是否对给定药品（按药品或类别）过敏。
func (a *Allergies) HasAllergy(patient, drug string, d Drug) bool {
	if a.drugs[patient][drug] {
		return true
	}
	return a.categories[patient][d.Category]
}
