package file

import (
	"mime"
	"strings"
)

// Category derives the display category from the recorded MIME, including legacy rows.
func Category(value string) string {
	value, _, _ = mime.ParseMediaType(strings.ToLower(value))
	switch value {
	case "application/pdf":
		return "document"
	case "text/csv", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "sheet"
	case "application/zip":
		return "archive"
	case "application/json", "application/x-ndjson", "application/xml", "application/yaml":
		return "text"
	}
	if strings.HasPrefix(value, "image/") {
		return "image"
	}
	if strings.HasPrefix(value, "text/") {
		return "text"
	}
	return "other"
}

func validCategory(value string) bool {
	return value == "image" || value == "document" || value == "sheet" || value == "archive" || value == "text" || value == "other"
}
