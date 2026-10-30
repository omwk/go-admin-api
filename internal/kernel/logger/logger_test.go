package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggerFormatLevelAndFixedFields(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			log, err := New(&output, "info", format)
			require.NoError(t, err)
			ctx := WithTraceID(context.Background(), "trace-example")
			log.DebugContext(ctx, "filtered")
			log.InfoContext(ctx, "visible", "module", "test", "action", "inspect")
			require.NotContains(t, output.String(), "filtered")
			for _, field := range []string{"ts", "level", "msg", "trace_id", "module", "action", "duration_ms", "error_code"} {
				require.Contains(t, output.String(), field)
			}
			require.Contains(t, output.String(), "trace-example")
			if format == "json" {
				var record map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &record))
				require.Equal(t, "test", record["module"])
				require.True(t, strings.HasSuffix(record["ts"].(string), "Z"))
			}
		})
	}
}

func TestLoggerRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ level, format, field string }{
		{"invalid", "json", "log.level"},
		{"info", "invalid", "log.format"},
	} {
		_, err := New(&bytes.Buffer{}, tc.level, tc.format)
		require.ErrorContains(t, err, tc.field)
	}
}
