package file

import (
	"bytes"
	"encoding/xml"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

var allowedMIME = map[string]map[string]struct{}{
	"image/jpeg": {".jpg": {}, ".jpeg": {}}, "image/png": {".png": {}},
	"image/webp": {".webp": {}}, "image/gif": {".gif": {}},
	"image/bmp": {".bmp": {}}, "image/tiff": {".tif": {}, ".tiff": {}},
	"image/x-icon": {".ico": {}}, "image/svg+xml": {".svg": {}},
	"application/pdf": {".pdf": {}}, "application/zip": {".zip": {}},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {".xlsx": {}},
	"text/csv": {".csv": {}},
}

func fileMIME(name string, prefix []byte) string {
	sniffed, _, err := mime.ParseMediaType(http.DetectContentType(prefix))
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".csv" && sniffed == "text/plain" {
		return "text/csv"
	}
	if ext == ".xlsx" && sniffed == "application/zip" {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	if ext == ".svg" {
		decoder := xml.NewDecoder(bytes.NewReader(prefix))
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			if start, ok := token.(xml.StartElement); ok {
				if start.Name.Local == "svg" {
					return "image/svg+xml"
				}
				break
			}
		}
	}
	return sniffed
}

func validateFile(name, declared, sniffed string, size int64, _ []byte) error {
	if size <= 0 {
		return invalidFile()
	}
	if size > MaxFileSize {
		return tooLargeOrInvalid()
	}
	if declared != "" {
		value, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(declared)))
		if err != nil || (value != sniffed && value != "application/octet-stream") {
			return invalidFile()
		}
	}
	exts, ok := allowedMIME[sniffed]
	if !ok {
		return invalidFile()
	}
	if _, ok := exts[strings.ToLower(filepath.Ext(name))]; !ok {
		return invalidFile()
	}
	return nil
}
