package catalog

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProductIncludeDescendantsQueryParsing(t *testing.T) {
	// BE-5 §4.7: both collection handlers use this parser.
	for _, tc := range []struct {
		query string
		want  bool
		valid bool
	}{
		{"", false, true},
		{"include_descendants=false", false, true},
		{"include_descendants=true", true, true},
		{"include_descendants=invalid", false, false},
		{"include_descendants=", false, false},
		{"include_descendants=1", false, false},
		{"include_descendants=null", false, false},
		{"include_descendants=true&include_descendants=false", false, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/products?"+tc.query, nil)
			var query ProductListQuery
			err := parseProductQuery(c, &query)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			raw, err := json.Marshal(query.ProductFilter)
			require.NoError(t, err)
			var filter map[string]any
			require.NoError(t, json.Unmarshal(raw, &filter))
			require.Equal(t, tc.want, filter["include_descendants"] == true)
		})
	}
}

func TestProductDescendantsCursorScope(t *testing.T) {
	// BE-5: descendant membership must be bound to the signed continuation.
	signer, err := NewProductCursorSigner(make([]byte, 32))
	require.NoError(t, err)
	query := ProductListQuery{PageSize: 20, Sort: "-updated_at"}
	require.NoError(t, json.Unmarshal([]byte(`{"category_id":1,"include_descendants":true}`), &query.ProductFilter))
	token, err := signer.Sign(1, query, ProductCursorPosition{ID: 42, UpdatedAt: time.Now().UTC()}, 101)
	require.NoError(t, err)
	_, err = signer.Parse(token, 1, query)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(`{"category_id":1,"include_descendants":false}`), &query.ProductFilter))
	_, err = signer.Parse(token, 1, query)
	require.Error(t, err, "switching to exact matching must invalidate a descendant cursor")
}

func TestProductDescendantsCursorIgnoresMissingCategory(t *testing.T) {
	signer, err := NewProductCursorSigner(make([]byte, 32))
	require.NoError(t, err)
	query, err := normalizeProductQuery(ProductListQuery{ProductFilter: ProductFilter{IncludeDescendants: true}})
	require.NoError(t, err)
	token, err := signer.Sign(1, query, ProductCursorPosition{ID: 42, UpdatedAt: time.Now().UTC()}, 101)
	require.NoError(t, err)
	plain, err := normalizeProductQuery(ProductListQuery{})
	require.NoError(t, err)
	_, err = signer.Parse(token, 1, plain)
	require.NoError(t, err, "the flag has no effect without category_id, including cursor scope")
}
