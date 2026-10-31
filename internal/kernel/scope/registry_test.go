package scope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func testSet() Set {
	root := Declaration{Entity: "root", Alias: "root", TenantColumn: "tenant_id", OwnerColumn: "owner_id", DepartmentColumn: "dept_id", DepartmentSource: "root.dept_id"}
	child := Declaration{Entity: "child", Alias: "child", TenantColumn: "tenant_id", DepartmentSource: "protected root", Parent: &Relation{Entity: "root", Alias: "parent", Column: "root_id", TargetColumn: "id"}}
	return Set{Entities: []Declaration{root, child}, Queries: map[string]QueryDeclaration{
		"Port.Read": {Roots: []Declaration{child}, Policy: "verified access"},
	}}
}

func TestRegistryRejectsInvalidDeclarations(t *testing.T) {
	// REQ-IAM-008: registration fails closed before adapters can execute queries.
	for _, tc := range []struct {
		name   string
		change func(*Set)
	}{
		{"duplicate entity", func(s *Set) { s.Entities = append(s.Entities, s.Entities[0]) }},
		{"unknown root", func(s *Set) {
			s.Queries["Port.Read"] = QueryDeclaration{Roots: []Declaration{{Entity: "unknown", Alias: "unknown"}}, Policy: "verified"}
		}},
		{"missing parent", func(s *Set) { s.Entities[1].Parent.Entity = "unknown" }},
		{"cycle", func(s *Set) {
			s.Entities[0].Parent = &Relation{Entity: "child", Alias: "parent", Column: "id", TargetColumn: "id"}
		}},
		{"missing ownership", func(s *Set) { s.Entities[0].OwnerColumn = "" }},
		{"invalid column", func(s *Set) { s.Entities[0].DepartmentColumn = "dept_id;" }},
		{"invalid alias", func(s *Set) { q := s.Queries["Port.Read"]; q.Roots[0].Alias = "child;"; s.Queries["Port.Read"] = q }},
		{"missing policy", func(s *Set) { s.Queries["Port.Read"] = QueryDeclaration{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSet()
			tc.change(&s)
			_, err := NewRegistry(s)
			require.Error(t, err)
		})
	}
	s := testSet()
	_, err := NewRegistry(s, Set{Queries: s.Queries})
	require.Error(t, err)
}

func TestRegistryFreezesInputsAndReturnedValues(t *testing.T) {
	s := testSet()
	r, err := NewRegistry(s)
	require.NoError(t, err)
	s.Entities[1].Parent.Entity = "changed"
	s.Queries["Port.Read"].Roots[0].Entity = "changed"
	q, err := r.Query("Port.Read")
	require.NoError(t, err)
	require.Equal(t, "child", q.Roots[0].Entity)
	require.Equal(t, "root", q.Roots[0].Parent.Entity)
	q.Roots[0].Parent.Entity = "changed"
	d, err := r.Lookup("child")
	require.NoError(t, err)
	require.Equal(t, "root", d.Parent.Entity)
	d.Parent.Entity = "changed"
	require.True(t, r.Allows("Port.Read", "child"))
	require.False(t, r.Allows("Port.Read", "root"))
	require.False(t, r.Allows("Port.Unknown", "child"))
	require.Equal(t, []string{"Read"}, r.QueryNames("Port"))
	_, err = r.Lookup("unknown")
	require.Error(t, err)
}

func TestAccessFactsRejectMissingIdentityAndRoles(t *testing.T) {
	for _, access := range []Access{{}, {Tenant: 1, User: 2}, {Tenant: 1, User: 2, Roles: []Role{{Kind: "unknown"}}}} {
		_, err := From(WithAccess(context.Background(), access))
		require.Error(t, err)
	}
	for _, kind := range []string{"all", "self", "dept", "dept_and_child", "custom"} {
		a := Access{Tenant: 1, User: 2, Roles: []Role{{Kind: kind}}}
		got, err := From(WithAccess(context.Background(), a))
		require.NoError(t, err)
		require.Equal(t, a, got)
	}
}
