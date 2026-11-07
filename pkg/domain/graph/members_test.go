package graph

import "testing"

func TestTypeUseEncode(t *testing.T) {
	for _, u := range []TypeUse{
		{Owner: "UserService", Member: "repo", Role: RoleField, Exported: false, Line: 12, Target: "app/repository", Type: "MySQLRepo"},
		{Owner: "Get", Member: "0", Role: RoleResult, Exported: true, Collection: true, Line: 3, Target: "src/infra/row.ts", Type: "Row"},
		{Owner: "UC", Member: "repo", Role: RoleField, Line: 4, Target: "app/infra", Type: "Repo", Calls: []string{"Find", "Save"}},
	} {
		got, ok := DecodeTypeUse(u.Encode())
		if !ok || got.Encode() != u.Encode() {
			t.Errorf("round trip %+v = %+v, %v", u, got, ok)
		}
	}
	for _, bad := range []string{"", "a\x1fb", "o\x1fm\x1fnope\x1f1\x1f0\x1f1\x1ft\x1fT", "o\x1fm\x1ffield\x1fx\x1f0\x1f1\x1ft\x1fT", "o\x1fm\x1ffield\x1f0\x1f0\x1f1\x1f\x1fT"} {
		if _, ok := DecodeTypeUse(bad); ok {
			t.Errorf("DecodeTypeUse(%q) accepted", bad)
		}
	}
}

func TestConstructEncode(t *testing.T) {
	c := Construct{Owner: "NewApp", Line: 9, Target: "app/infra", Type: "Store"}
	if got, ok := DecodeConstruct(c.Encode()); !ok || got != c {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	if _, ok := DecodeConstruct("x"); ok {
		t.Error("accepted garbage")
	}
}

func TestTypeRefString(t *testing.T) {
	r := TypeRef{Target: "app/repository", Type: "UserRepository"}
	if got, ok := ParseTypeRef(r.String()); !ok || got != r {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	if _, ok := ParseTypeRef("nohash"); ok {
		t.Error("accepted a ref without #")
	}
}

func TestDeclRefEncode(t *testing.T) {
	d := DeclRef{Name: "parse", Refs: []string{"lex", "Token"}}
	if got, ok := DecodeDeclRef(d.Encode()); !ok || got.Name != d.Name || len(got.Refs) != 2 || got.Refs[1] != "Token" {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	if got, ok := DecodeDeclRef(DeclRef{Name: "x"}.Encode()); !ok || got.Name != "x" || len(got.Refs) != 0 {
		t.Errorf("no refs = %+v, %v", got, ok)
	}
	if _, ok := DecodeDeclRef(""); ok {
		t.Error("accepted an empty string")
	}
}
