package ontology

import (
	"fmt"
	"slices"
)

func validateWritePolicy(policy WritePolicy) error {
	if policy != RejectObject && policy != IgnoreField {
		return newError(KindWritePolicyConflict, "exactly one type-wide write policy is required")
	}
	return nil
}

func validateProperties(properties []Property, seen map[string]bool) error {
	if seen == nil {
		seen = map[string]bool{}
	}
	for _, property := range properties {
		if property.LineageID == "" || property.Identifier == "" {
			return newError(KindInvalidInput, "property lineage id and identifier are required")
		}
		if seen[property.Identifier] {
			return newError(KindInvalidInput, "duplicate property identifier: "+property.Identifier)
		}
		seen[property.Identifier] = true
	}
	return nil
}

func evolveProperties(current *TypeVersion, evolution Evolution) ([]Property, error) {
	properties := append([]Property(nil), current.Properties...)
	for _, rename := range evolution.Rename {
		if rename.To == "" || rename.To == rename.From {
			return nil, newError(KindInvalidInput, "rename target must be a new non-empty identifier")
		}
	}
	for _, constraint := range evolution.Tighten {
		if constraint.Constraint == "" {
			return nil, newError(KindInvalidInput, "tightened constraint must not be empty")
		}
	}
	if err := validateProperties(evolution.Add, nil); err != nil {
		return nil, err
	}
	result := make([]Property, 0, len(properties)+len(evolution.Add))
	for _, property := range properties {
		deprecated := false
		for _, deprecatedName := range evolution.Deprecate {
			if property.Identifier == deprecatedName {
				deprecated = true
				break
			}
		}
		if deprecated {
			continue
		}
		for _, rename := range evolution.Rename {
			if property.Identifier == rename.From {
				property.Identifier = rename.To
			}
		}
		for _, change := range evolution.Tighten {
			if property.Identifier == change.Property {
				property.Constraint = change.Constraint
			}
		}
		result = append(result, property)
	}

	resultByID := map[string]bool{}
	for _, property := range result {
		if resultByID[property.Identifier] {
			return nil, newError(KindInvalidInput, "duplicate property identifier after evolution: "+property.Identifier)
		}
		resultByID[property.Identifier] = true
	}
	currentByID := map[string]bool{}
	for identifier := range current.byName {
		currentByID[identifier] = true
	}

	referenced := map[string]bool{}
	for _, rename := range evolution.Rename {
		if referenced[rename.From] {
			return nil, newError(KindInvalidInput, "property listed more than once in one evolution: "+rename.From)
		}
		referenced[rename.From] = true
		if _, existed := current.byName[rename.From]; !existed {
			return nil, newError(KindPropertyNotFound, "property identifier does not exist: "+rename.From)
		}
		if rename.To == rename.From || currentByID[rename.To] {
			return nil, newError(KindInvalidInput, "property identifier already exists: "+rename.To)
		}
	}
	for _, deprecated := range evolution.Deprecate {
		if _, existed := current.byName[deprecated]; !existed {
			return nil, newError(KindPropertyNotFound, "property identifier does not exist: "+deprecated)
		}
	}
	for _, constraint := range evolution.Tighten {
		if _, exists := resultByID[constraint.Property]; !exists {
			return nil, newError(KindPropertyNotFound, "property identifier does not exist: "+constraint.Property)
		}
	}
	for _, added := range evolution.Add {
		if resultByID[added.Identifier] {
			return nil, newError(KindInvalidInput, "property identifier already exists: "+added.Identifier)
		}
		resultByID[added.Identifier] = true
		result = append(result, added)
	}
	if err := validateProperties(result, nil); err != nil {
		return nil, err
	}
	return result, nil
}

func validOperation(operation Operation) bool {
	return operation == Read || operation == Write
}

func contains[T comparable](values []T, target T) bool {
	return slices.Contains(values, target)
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprint(names)
}
