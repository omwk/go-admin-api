package identity

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestMenuTreeFilteringAndOrdering(t *testing.T) {
	// REQ-IAM-006: inaccessible ancestors suppress descendants, including visible children.
	rows := []model.Menu{
		{ID: 1, ParentID: 0, Type: "dir", Status: "active", Visible: true, Sort: 2},
		{ID: 2, ParentID: 1, Type: "menu", Status: "active", Visible: true, Sort: 1},
		{ID: 3, ParentID: 1, Type: "button", Status: "active", Visible: true},
		{ID: 4, ParentID: 0, Type: "dir", Status: "active", Visible: false},
		{ID: 5, ParentID: 4, Type: "menu", Status: "active", Visible: true},
		{ID: 6, ParentID: 0, Type: "menu", Status: "disabled", Visible: true},
		{ID: 7, ParentID: 0, Type: "menu", Status: "active", Visible: true, Sort: 1},
		{ID: 8, ParentID: 1, Type: "menu", Status: "active", Visible: true, Sort: 1},
	}
	tree := BuildMenuTree(rows, []int64{1, 2, 3, 4, 5, 6, 7, 8}, false)
	require.Len(t, tree, 3)
	require.EqualValues(t, 4, tree[0].ID)
	require.EqualValues(t, 5, tree[0].Children[0].ID)
	require.EqualValues(t, 7, tree[1].ID)
	require.EqualValues(t, 1, tree[2].ID)
	require.Equal(t, []int64{2, 8}, []int64{tree[2].Children[0].ID, tree[2].Children[1].ID})
	require.Empty(t, BuildMenuTree(rows, []int64{2, 5}, false))
	require.Equal(t, tree, BuildMenuTree(rows, nil, true))
}

func TestMenuTreeCapsDepthAtFiveAndDropsCycles(t *testing.T) {
	rows := []model.Menu{}
	for id := int64(1); id <= 6; id++ {
		rows = append(rows, model.Menu{ID: id, ParentID: id - 1, Type: "dir", Status: "active", Visible: true})
	}
	rows = append(rows, model.Menu{ID: 7, ParentID: 8, Type: "dir", Status: "active", Visible: true}, model.Menu{ID: 8, ParentID: 7, Type: "dir", Status: "active", Visible: true})
	tree := BuildMenuTree(rows, nil, true)
	for depth := 1; depth < 5; depth++ {
		require.Len(t, tree, 1)
		tree = tree[0].Children
	}
	require.Len(t, tree, 1)
	require.EqualValues(t, 5, tree[0].ID)
	require.Empty(t, tree[0].Children)
}

// FE-IDN-009: visibility is presentation only; grants/status still control routes.
func TestMenuTreeProjectsHiddenPagesWithGrants(t *testing.T) {
	path, permission := "/catalog/products/:id", "product:spu:read"
	rows := []model.Menu{
		{ID: 1, Type: "dir", Status: "active", Visible: true},
		{ID: 2, ParentID: 1, Type: "menu", Status: "active", Visible: false, Path: &path, PermissionCode: &permission},
		{ID: 3, ParentID: 1, Type: "menu", Status: "disabled", Visible: false},
		{ID: 4, ParentID: 1, Type: "button", Status: "active", Visible: false},
	}
	for _, tc := range []struct {
		name   string
		grants []int64
		count  int
	}{
		{"granted hidden page", []int64{1, 2, 3, 4}, 1},
		{"ungranted hidden page", []int64{1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := BuildMenuTree(rows, tc.grants, false)
			require.Len(t, tree, 1)
			require.Len(t, tree[0].Children, tc.count)
			if tc.count == 0 {
				return
			}
			require.Equal(t, &path, tree[0].Children[0].Path)
			require.Equal(t, &permission, tree[0].Children[0].PermissionCode)
			data, err := json.Marshal(tree)
			require.NoError(t, err)
			var projected []struct {
				Visible  *bool
				Children []struct{ Visible *bool }
			}
			require.NoError(t, json.Unmarshal(data, &projected))
			require.NotNil(t, projected[0].Visible)
			require.True(t, *projected[0].Visible)
			require.NotNil(t, projected[0].Children[0].Visible)
			require.False(t, *projected[0].Children[0].Visible)
		})
	}
	require.Empty(t, BuildMenuTree(rows, []int64{2}, false))
}
