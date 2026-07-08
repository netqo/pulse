package seed

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public Binance market-data archive host.
const DefaultBaseURL = "https://data.binance.vision"

// maxArchiveBytes caps a single archive download to guard against a runaway
// response. Monthly 1m kline archives are a few MB; 64 MiB is generous.
const maxArchiveBytes = 64 << 20

// HTTPFetcher downloads monthly spot kline archives from a Binance-vision-style
// host, verifies each archive against its published SHA256 checksum, and parses
// the CSV inside.
type HTTPFetcher struct {
	client  *http.Client
	baseURL string
}

// NewHTTPFetcher builds a fetcher against baseURL (DefaultBaseURL when empty),
// bounding every request by timeout.
func NewHTTPFetcher(baseURL string, timeout time.Duration) *HTTPFetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &HTTPFetcher{
		client:  &http.Client{Timeout: timeout},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// FetchMonth downloads and parses the monthly kline archive for symbol and
// interval, returning ErrNoData when the archive is absent (404).
func (f *HTTPFetcher) FetchMonth(ctx context.Context, symbol, interval string, month time.Time) ([]Kline, error) {
	name := fmt.Sprintf("%s-%s-%s", symbol, interval, month.Format("2006-01"))
	zipURL := fmt.Sprintf("%s/data/spot/monthly/klines/%s/%s/%s.zip", f.baseURL, symbol, interval, name)

	archive, err := f.download(ctx, zipURL)
	if err != nil {
		return nil, err
	}
	if err := f.verifyChecksum(ctx, zipURL+".CHECKSUM", archive); err != nil {
		return nil, err
	}
	return parseKlineZip(archive)
}

// download GETs url and returns its body, mapping 404 to ErrNoData. Errors are
// left unprefixed; the Seeder adds the single "seed:" context around them.
func (f *HTTPFetcher) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoData
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: unexpected status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxArchiveBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", url, maxArchiveBytes)
	}
	return body, nil
}

// verifyChecksum downloads the archive's .CHECKSUM sidecar and confirms the
// archive's SHA256 matches, guaranteeing integrity of the downloaded data.
func (f *HTTPFetcher) verifyChecksum(ctx context.Context, checksumURL string, archive []byte) error {
	body, err := f.download(ctx, checksumURL)
	if err != nil {
		if errors.Is(err, ErrNoData) {
			return errors.New("checksum sidecar missing")
		}
		return err
	}
	want, err := parseChecksum(body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// parseChecksum extracts the hex SHA256 from a ".CHECKSUM" file, whose format is
// "<hex>  <filename>".
func parseChecksum(content []byte) (string, error) {
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return "", errors.New("empty checksum file")
	}
	sum := strings.ToLower(fields[0])
	if len(sum) != 64 {
		return "", fmt.Errorf("malformed checksum %q", fields[0])
	}
	return sum, nil
}

// parseKlineZip reads the single CSV inside a kline archive into klines.
func parseKlineZip(archive []byte) ([]Kline, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	for _, file := range zr.File {
		if !strings.HasSuffix(file.Name, ".csv") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", file.Name, err)
		}
		defer func() { _ = rc.Close() }()
		return parseKlineCSV(rc)
	}
	return nil, errors.New("archive has no CSV entry")
}

// parseKlineCSV parses Binance kline CSV rows, tolerating an optional header row
// and both millisecond and microsecond open-time encodings.
func parseKlineCSV(r io.Reader) ([]Kline, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // kline schemas vary in trailing columns

	var klines []Kline
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv: %w", err)
		}
		if len(record) < 6 {
			return nil, fmt.Errorf("short kline row with %d fields", len(record))
		}

		openRaw, err := strconv.ParseInt(strings.TrimSpace(record[0]), 10, 64)
		if err != nil {
			// A non-numeric first field is the header row; skip it.
			continue
		}
		klines = append(klines, Kline{
			OpenTime: epochToTime(openRaw),
			Close:    record[4],
			Volume:   record[5],
		})
	}
	return klines, nil
}

// epochToTime converts a Binance epoch timestamp to UTC, detecting whether it is
// expressed in microseconds (used since 2025), milliseconds, or seconds by
// magnitude.
func epochToTime(v int64) time.Time {
	switch {
	case v >= 1e15:
		return time.UnixMicro(v).UTC()
	case v >= 1e12:
		return time.UnixMilli(v).UTC()
	default:
		return time.Unix(v, 0).UTC()
	}
}
