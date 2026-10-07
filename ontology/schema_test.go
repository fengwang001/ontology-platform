package ontology

import "testing"

func TestCyclicDerivationRejectedBeforeActivation(t *testing.T) {
	s := NewSchema()
	mustReg(s.RegisterObjectType("Node", []PropertyName{"x", "y"}))
	mustReg(s.RegisterLinkType(LinkType{Name: "edge", SrcType: "Node", DstType: "Node"}))
	mustReg(s.RegisterDerivedIndex(DerivedIndex{
		SrcType: "Node", PropName: "x", Link: "edge", DstProp: "y"}))

	err := s.RegisterDerivedIndex(DerivedIndex{
		SrcType: "Node", PropName: "y", Link: "edge", DstProp: "x"})
	if ErrorKindOf(err) != KindCyclicDerivation {
		t.Fatalf("want cyclic rejection before activation, got %v", err)
	}
	if _, derived := s.derivedIndex("Node", "y"); derived {
		t.Fatal("rejected cyclic derivation must not be activated")
	}

	s2 := NewSchema()
	mustReg(s2.RegisterObjectType("N", []PropertyName{"a", "b", "c"}))
	mustReg(s2.RegisterLinkType(LinkType{Name: "e", SrcType: "N", DstType: "N"}))
	mustReg(s2.RegisterDerivedIndex(DerivedIndex{SrcType: "N", PropName: "a", Link: "e", DstProp: "b"}))
	mustReg(s2.RegisterDerivedIndex(DerivedIndex{SrcType: "N", PropName: "b", Link: "e", DstProp: "c"}))
	err = s2.RegisterDerivedIndex(DerivedIndex{SrcType: "N", PropName: "c", Link: "e", DstProp: "a"})
	if ErrorKindOf(err) != KindCyclicDerivation {
		t.Fatalf("want long-cycle rejection, got %v", err)
	}
}

func TestUnsupportedLinkTypeRejected(t *testing.T) {
	s := NewSchema()
	mustReg(s.RegisterObjectType("A", []PropertyName{"p"}))
	mustReg(s.RegisterObjectType("B", []PropertyName{"q"}))
	err := s.RegisterLinkType(LinkType{Name: "ab", SrcType: "A", DstType: "B"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.RegisterDerivedIndex(DerivedIndex{SrcType: "A", PropName: "p", Link: "ghost", DstProp: "q"})
	if ErrorKindOf(err) != KindLinkTypeNotSupported {
		t.Fatalf("want link-not-supported, got %v", err)
	}
}
