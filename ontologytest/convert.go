package ontologytest

import "ontology/ontology"

func oid(id ID) ontology.ObjectID          { return ontology.ObjectID(id) }
func opn(p PropName) ontology.PropertyName { return ontology.PropertyName(p) }
func otn(t TypeName) ontology.ObjectTypeName {
	return ontology.ObjectTypeName(t)
}
func oln(l LinkName) ontology.LinkTypeName { return ontology.LinkTypeName(l) }
