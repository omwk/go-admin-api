package file

import (
	"bytes"
	"encoding/xml"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

var allowedMIME = map[string]map[string]struct{}{
	"image/jpeg": {".jpg": {}, ".jpeg": {}}, "image/png": {".png": {}},
	"image/webp": {".webp": {}}, "image/gif": {".gif": {}},
	"image/bmp": {".bmp": {}}, "image/tiff": {".tif": {}, ".tiff": {}},
	"image/x-icon": {".ico": {}}, "image/svg+xml": {".svg": {}},
	"application/pdf": {".pdf": {}}, "application/zip": {".zip": {}},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {".xlsx": {}},
	"text/csv":      {".csv": {}},
	"text/plain":    {".txt": {}, ".log": {}, ".md": {}, ".json": {}, ".ndjson": {}, ".jsonl": {}, ".xml": {}, ".yaml": {}, ".yml": {}},
	"text/markdown": {".md": {}}, "application/json": {".json": {}},
	"application/x-ndjson": {".ndjson": {}, ".jsonl": {}, ".log": {}},
	"text/xml":             {".xml": {}}, "application/xml": {".xml": {}},
	"application/yaml": {".yaml": {}, ".yml": {}}, "text/yaml": {".yaml": {}, ".yml": {}},
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
		return emptyFile()
	}
	if size > MaxFileSize {
		return tooLargeOrInvalid()
	}
	if declared != "" {
		value, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(declared)))
		if err != nil || !compatibleMIME(value, sniffed) {
			return unsupportedFileType(sniffed)
		}
	}
	exts, ok := allowedMIME[sniffed]
	if !ok {
		return unsupportedFileType(sniffed)
	}
	if _, ok := exts[strings.ToLower(filepath.Ext(name))]; !ok {
		return unsupportedFileType(sniffed)
	}
	return nil
}

func compatibleMIME(declared, sniffed string) bool {
	if declared == sniffed || declared == "application/octet-stream" {
		return true
	}
	if sniffed == "text/xml" && declared == "application/xml" {
		return true
	}
	if sniffed != "text/plain" {
		return false
	}
	// These declarations describe plain text; metadata still records the sniffed type.
	switch declared {
	case "text/markdown", "application/json", "application/x-ndjson", "text/xml", "application/xml", "application/yaml", "text/yaml":
		return true
	}
	return false
}

func unsupportedFileType(sniffed string) error {
	allowed := make([]string, 0, len(allowedMIME))
	for value := range allowedMIME {
		allowed = append(allowed, value)
	}
	sort.Strings(allowed)
	err := errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "不支持的文件类型："+sniffed)
	err.Details = map[string]any{"mime": sniffed, "allowed": allowed}
	return err
}

func emptyFile() error {
	return errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "文件不能为空")
}

func emptyFilename() error {
	return errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "文件名不能为空")
}
