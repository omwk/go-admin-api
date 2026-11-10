package billing

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChargeAmount(t *testing.T) {
	// REQ-BILL-002: exact integer cents, never floating point or overflow.
	for _, tc := range []struct {
		name               string
		price, units, want int64
		invalid            bool
	}{
		{"fixed", 5, 1, 5, false},
		{"multiple", 5, 3, 15, false},
		{"max", math.MaxInt64, 1, math.MaxInt64, false},
		{"overflow", math.MaxInt64, 2, 0, true},
		{"negative", -1, 1, 0, true},
		{"zero price", 0, 1, 0, true},
		{"zero units", 5, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChargeAmount(tc.price, tc.units)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
