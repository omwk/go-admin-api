package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

var exportHeader = []string{"id", "title", "price_min_cents", "price_max_cents", "sku_count", "category_id", "category", "status", "updated_at"}

func exportCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

type exportWriter struct {
	target io.Writer
	hash   hash.Hash
	size   int64
}

func (w *exportWriter) Write(data []byte) (int, error) {
	n, err := w.target.Write(data)
	if _, hashErr := w.hash.Write(data[:n]); hashErr != nil {
		return n, errors.Join(err, hashErr)
	}
	w.size += int64(n)
	return n, err
}

func (s *ExportService) writeCSV(ctx context.Context, job ExportJob, target io.Writer) (ExportDigest, error) {
	output := &exportWriter{target: target, hash: sha256.New()}
	writer := csv.NewWriter(output)
	if err := writer.Write(exportHeader); err != nil {
		return ExportDigest{}, err
	}
	var ordinal, count int64
	for {
		if err := ctx.Err(); err != nil {
			return ExportDigest{}, err
		}
		rows, err := s.store.ExportRows(ctx, job, ordinal)
		if err != nil {
			return ExportDigest{}, err
		}
		if len(rows) > 1000 {
			return ExportDigest{}, errors.New("export batch exceeds limit")
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if row.Ordinal != ordinal+1 {
				return ExportDigest{}, errors.New("export ordinal is not contiguous")
			}
			record, err := exportRecord(row)
			if err != nil {
				return ExportDigest{}, err
			}
			if err := writer.Write(record); err != nil {
				return ExportDigest{}, err
			}
			ordinal, count = row.Ordinal, count+1
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return ExportDigest{}, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportDigest{}, err
	}
	if count != job.RowTotal {
		return ExportDigest{}, errors.New("export materialized row count mismatch")
	}
	return ExportDigest{Size: output.size, Rows: count, SHA256: hex.EncodeToString(output.hash.Sum(nil))}, nil
}

func exportRecord(row ExportRow) ([]string, error) {
	var item ExportProjection
	if err := json.Unmarshal(row.Projection, &item); err != nil {
		return nil, errors.New("invalid export projection")
	}
	category := ""
	if item.CategoryID != nil {
		category = strconv.FormatInt(*item.CategoryID, 10)
	}
	values := []string{strconv.FormatInt(item.ID, 10), item.Title, strconv.FormatInt(item.PriceMinCents, 10),
		strconv.FormatInt(item.PriceMaxCents, 10), strconv.Itoa(item.SKUCount), category, item.Category, item.Status, item.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	for i := range values {
		values[i] = exportCell(values[i])
	}
	return values, nil
}

func (s *ExportService) verifyObject(ctx context.Context, key string, expected ExportDigest) (retErr error) {
	object, err := s.objects.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, object.Body.Close()) }()
	if object.Size != expected.Size {
		return errors.New("export object size mismatch")
	}
	digest := sha256.New()
	reader := csv.NewReader(io.TeeReader(io.LimitReader(object.Body, expected.Size+1), digest))
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, exportHeader) {
		return errors.New("export object header mismatch")
	}
	var rows int64
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("export object CSV invalid")
		}
		rows++
	}
	if rows != expected.Rows || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
		return errors.New("export object digest mismatch")
	}
	return nil
}
