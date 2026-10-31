package identity

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestAdminMenuMoveValidatesEntireGraph(t *testing.T) {
	// REQ-IAM-006: hidden/disabled descendants remain part of structural validation.
	rows := []model.Menu{{ID: 1}, {ID: 2, ParentID: 1}, {ID: 3, ParentID: 2}, {ID: 4, ParentID: 3, Status: "disabled"}, {ID: 5, ParentID: 4}, {ID: 6}}
	for _, tc := range []struct {
		name       string
		id, parent int64
		code       int
	}{
		{"self", 1, 1, 20106}, {"descendant", 1, 5, 20106},
		{"whole subtree too deep", 1, 6, 10001}, {"missing parent", 6, 99, 10003},
		{"missing target", 99, 0, 10003}, {"move to root", 2, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMenuMove(rows, tc.id, tc.parent)
			if tc.code == 0 {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.code, errs.From(err).Code)
			}
		})
	}
}
