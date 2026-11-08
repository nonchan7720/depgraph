package graph

import "github.com/nonchan7720/depgraph/pkg/domain/graph"

// usageProps are the File node properties holding fg.Usage (the input of
// the unused-code rules). Every key is always present: a nil value removes
// the property (`SET f += $usage`), so a file re-parsed without usage keeps
// nothing stale. Lists are stored as encoded strings, see graph.Use.Encode.
func usageProps(fg *graph.FileGraph) map[string]any {
	props := map[string]any{"scope": nil, "entry": nil, "internal": nil, "generated": nil, "uses": nil, "exports": nil, "keep": nil, "mentions": nil,
		"typeUses": nil, "constructs": nil, "decls": nil, "idents": nil, "declRefs": nil}
	if len(fg.TypeUses) > 0 {
		us := make([]string, len(fg.TypeUses))
		for i, x := range fg.TypeUses {
			us[i] = x.Encode()
		}
		props["typeUses"] = us
	}
	if len(fg.Constructs) > 0 {
		cs := make([]string, len(fg.Constructs))
		for i, x := range fg.Constructs {
			cs[i] = x.Encode()
		}
		props["constructs"] = cs
	}
	u := fg.Usage
	if u == nil {
		return props
	}
	if len(u.Decls) > 0 {
		props["decls"] = u.Decls
	}
	if len(u.Idents) > 0 {
		props["idents"] = u.Idents
	}
	if len(u.DeclRefs) > 0 {
		ds := make([]string, len(u.DeclRefs))
		for i, d := range u.DeclRefs {
			ds[i] = d.Encode()
		}
		props["declRefs"] = ds
	}
	props["scope"] = u.Scope
	if u.Entry != "" {
		props["entry"] = u.Entry
	}
	if u.Internal {
		props["internal"] = true
	}
	if u.Generated {
		props["generated"] = true
	}
	if len(u.Uses) > 0 {
		uses := make([]string, len(u.Uses))
		for i, x := range u.Uses {
			uses[i] = x.Encode()
		}
		props["uses"] = uses
	}
	if len(u.Exports) > 0 {
		exp := make([]string, len(u.Exports))
		for i, e := range u.Exports {
			exp[i] = e.Encode()
		}
		props["exports"] = exp
	}
	if len(u.Keep) > 0 {
		props["keep"] = u.Keep
	}
	if len(u.Mentions) > 0 {
		props["mentions"] = u.Mentions
	}
	return props
}
