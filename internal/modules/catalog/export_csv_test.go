package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportCellPreventsFormulaInjection(t *testing.T) {
	// REQ-PRD-008: spreadsheet formula prefixes must be treated as text.
	for _, value := range []string{"=1+1", "+SUM(A1)", "-1+1", "@SUM(A1)", "\t=1+1", "\r=1+1", " \t=1+1"} {
		require.Equal(t, "'"+value, exportCell(value))
	}
	for _, value := range []string{"", "normal", "中文", "1+1", "a,b\"c"} {
		require.Equal(t, value, exportCell(value))
	}
}
