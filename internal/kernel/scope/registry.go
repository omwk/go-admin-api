package scope

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Relation fixes a child column and its protected parent key at build time.
type Relation struct {
	Entity, Alias, Column, TargetColumn string
}

// Declaration fixes an entity's ownership and department mapping.
type Declaration struct {
	Entity, Alias, TenantColumn, OwnerColumn, DepartmentSource string
	Shared                                                     bool
	DepartmentColumn, DeletedColumn                            string
	Parent                                                     *Relation
	Global                                                     bool
}

// QueryDeclaration identifies a repository method's roots and boundary policy.
type QueryDeclaration struct {
	Roots  []Declaration
	Policy string
}

// Set is a module's static entity and method declarations.
type Set struct {
	Entities []Declaration
	Queries  map[string]QueryDeclaration
}

// Registry is a validated, immutable snapshot of merged module declarations.
type Registry struct {
	entities map[string]Declaration
	queries  map[string]QueryDeclaration
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var queryName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*\.[A-Za-z][A-Za-z0-9]*$`)

// NewRegistry merges, validates and freezes declarations before serving requests.
func NewRegistry(sets ...Set) (*Registry, error) {
	r := &Registry{entities: map[string]Declaration{}, queries: map[string]QueryDeclaration{}}
	for _, set := range sets {
		for _, d := range set.Entities {
			if _, exists := r.entities[d.Entity]; exists {
				return nil, errors.New("duplicate scope entity")
			}
			if err := validateDeclaration(d); err != nil {
				return nil, err
			}
			r.entities[d.Entity] = cloneDeclaration(d)
		}
	}
	for _, d := range r.entities {
		if err := r.validateParents(d); err != nil {
			return nil, err
		}
	}
	for _, set := range sets {
		for name, q := range set.Queries {
			if err := r.addQuery(name, q); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

func validateDeclaration(d Declaration) error {
	if !identifier.MatchString(d.Entity) || !identifier.MatchString(d.Alias) || d.DepartmentSource == "" {
		return errors.New("invalid scope entity mapping")
	}
	if d.Global {
		if d.TenantColumn != "" || d.OwnerColumn != "" || d.Shared || d.Parent != nil || d.DepartmentColumn != "" {
			return errors.New("invalid global scope policy")
		}
		return nil
	}
	if !identifier.MatchString(d.TenantColumn) {
		return errors.New("scope tenant column is required")
	}
	for _, column := range []string{d.OwnerColumn, d.DepartmentColumn, d.DeletedColumn} {
		if column != "" && !identifier.MatchString(column) {
			return errors.New("invalid scope column")
		}
	}
	if !d.Shared && d.Parent == nil && (d.OwnerColumn == "" || d.DepartmentColumn == "") {
		return errors.New("scope ownership or parent mapping is required")
	}
	return nil
}

func (r *Registry) validateParents(d Declaration) error {
	seen := map[string]bool{d.Entity: true}
	for d.Parent != nil {
		p := d.Parent
		if !identifier.MatchString(p.Alias) || !identifier.MatchString(p.Column) || !identifier.MatchString(p.TargetColumn) {
			return errors.New("invalid scope parent relation")
		}
		parent, exists := r.entities[p.Entity]
		if !exists || parent.Global || parent.Shared || seen[p.Entity] {
			return errors.New("missing or cyclic scope parent relation")
		}
		seen[p.Entity], d = true, parent
	}
	return nil
}

func (r *Registry) addQuery(name string, q QueryDeclaration) error {
	if !queryName.MatchString(name) || q.Policy == "" {
		return errors.New("invalid scope method declaration")
	}
	if _, exists := r.queries[name]; exists {
		return errors.New("duplicate scope method")
	}
	result := QueryDeclaration{Policy: q.Policy}
	for _, root := range q.Roots {
		d, exists := r.entities[root.Entity]
		if !exists || !identifier.MatchString(root.Alias) {
			return errors.New("scope method references unknown entity or invalid alias")
		}
		d.Alias = root.Alias
		result.Roots = append(result.Roots, cloneDeclaration(d))
	}
	r.queries[name] = result
	return nil
}

// Lookup rejects entities absent from the frozen registry.
func (r *Registry) Lookup(entity string) (Declaration, error) {
	d, ok := r.entities[entity]
	if !ok {
		return Declaration{}, Denied()
	}
	return cloneDeclaration(d), nil
}

// Query rejects undeclared repository methods and returns an independent snapshot.
func (r *Registry) Query(name string) (QueryDeclaration, error) {
	q, ok := r.queries[name]
	if !ok {
		return QueryDeclaration{}, Denied()
	}
	result := QueryDeclaration{Policy: q.Policy}
	for _, root := range q.Roots {
		result.Roots = append(result.Roots, cloneDeclaration(root))
	}
	return result, nil
}

// QueryNames returns a stable list for exact port reflection comparisons.
func (r *Registry) QueryNames(port string) []string {
	result := []string{}
	for name := range r.queries {
		if method, ok := strings.CutPrefix(name, port+"."); ok {
			result = append(result, method)
		}
	}
	sort.Strings(result)
	return result
}

// Allows binds a fixed adapter entity to its declared method.
func (r *Registry) Allows(method, entity string) bool {
	q, err := r.Query(method)
	if err != nil {
		return false
	}
	for _, root := range q.Roots {
		if root.Entity == entity {
			return true
		}
	}
	return false
}

func cloneDeclaration(d Declaration) Declaration {
	if d.Parent != nil {
		parent := *d.Parent
		d.Parent = &parent
	}
	return d
}
