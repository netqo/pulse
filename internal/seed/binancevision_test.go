package seed

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEpochToTime(t *testing.T) {
	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := epochToTime(1704067200000); !got.Equal(want) {
		t.Errorf("milliseconds: got %v, want %v", got, want)
	}
	if got := epochToTime(1704067200000000); !got.Equal(want) {
		t.Errorf("microseconds: got %v, want %v", got, want)
	}
	if got := epochToTime(1704067200); !got.Equal(want) {
		t.Errorf("seconds: got %v, want %v", got, want)
	}
}

func TestParseChecksum(t *testing.T) {
	valid := strings.Repeat("a", 64)
	got, err := parseChecksum([]byte(valid + "  BTCUSDT-1m-2024-01.zip\n"))
	if err != nil {
		t.Fatalf("parseChecksum: %v", err)
	}
	if got != valid {
		t.Errorf("checksum = %q, want %q", got, valid)
	}
	if _, err := parseChecksum([]byte("tooshort  file.zip")); err == nil {
		t.Error("expected error for malformed checksum")
	}
	if _, err := parseChecksum([]byte("")); err == nil {
		t.Error("expected error for empty checksum file")
	}
}

func TestParseKlineCSV(t *testing.T) {
	csv := strings.Join([]string{
		"open_time,open,high,low,close,volume,close_time", // header, must be skipped
		"1704067200000,42000.00,42100.00,41900.00,42050.10000000,12.50000000,1704067259999",
		"1704067260000000,42050.10,42090.00,42040.00,42075.20000000,3.20000000,1704067319999999", // microseconds
	}, "\n")

	klines, err := parseKlineCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parseKlineCSV: %v", err)
	}
	if len(klines) != 2 {
		t.Fatalf("parsed %d klines, want 2 (header skipped)", len(klines))
	}
	if !klines[0].OpenTime.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("kline 0 time = %v", klines[0].OpenTime)
	}
	if klines[0].Close != "42050.10000000" || klines[0].Volume != "12.50000000" {
		t.Errorf("kline 0 close/volume = %q/%q", klines[0].Close, klines[0].Volume)
	}
	if !klines[1].OpenTime.Equal(time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC)) {
		t.Errorf("kline 1 time = %v (microsecond decode)", klines[1].OpenTime)
	}
}

// makeKlineZip builds an in-memory zip archive containing one CSV file.
func makeKlineZip(t *testing.T, csvName, csvBody string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(csvName)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(csvBody)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// archiveServer serves a single month's zip and its checksum. corruptChecksum
// forces a mismatch to exercise the integrity check.
func archiveServer(t *testing.T, zipBytes []byte, corruptChecksum bool) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(zipBytes)
	checksum := hex.EncodeToString(sum[:])
	if corruptChecksum {
		checksum = strings.Repeat("0", 64)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "BTCUSDT-1m-2024-01.zip.CHECKSUM"):
			_, _ = w.Write([]byte(checksum + "  BTCUSDT-1m-2024-01.zip\n"))
		case strings.HasSuffix(r.URL.Path, "BTCUSDT-1m-2024-01.zip"):
			_, _ = w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestHTTPFetcherFetchMonth(t *testing.T) {
	csvBody := "1704067200000,42000.00,42100.00,41900.00,42050.10000000,12.50000000,1704067259999\n"
	zipBytes := makeKlineZip(t, "BTCUSDT-1m-2024-01.csv", csvBody)
	srv := archiveServer(t, zipBytes, false)
	defer srv.Close()

	fetcher := NewHTTPFetcher(srv.URL, 5*time.Second)
	month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	klines, err := fetcher.FetchMonth(context.Background(), "BTCUSDT", "1m", month)
	if err != nil {
		t.Fatalf("FetchMonth: %v", err)
	}
	if len(klines) != 1 || klines[0].Close != "42050.10000000" {
		t.Fatalf("klines = %+v, want one row with close 42050.10000000", klines)
	}

	// A month with no archive is a skippable gap, not an error.
	feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fetcher.FetchMonth(context.Background(), "BTCUSDT", "1m", feb); !errors.Is(err, ErrNoData) {
		t.Errorf("missing month err = %v, want ErrNoData", err)
	}
}

func TestHTTPFetcherRejectsMissingChecksum(t *testing.T) {
	zipBytes := makeKlineZip(t, "BTCUSDT-1m-2024-01.csv",
		"1704067200000,1,1,1,1.00000000,1.00000000,1704067259999\n")
	// Serve the zip but 404 the .CHECKSUM sidecar.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") && !strings.HasSuffix(r.URL.Path, ".CHECKSUM") {
			_, _ = w.Write(zipBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher(srv.URL, 5*time.Second)
	month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := fetcher.FetchMonth(context.Background(), "BTCUSDT", "1m", month)
	if err == nil || !strings.Contains(err.Error(), "checksum sidecar missing") {
		t.Fatalf("err = %v, want 'checksum sidecar missing'", err)
	}
}

func TestHTTPFetcherRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher(srv.URL, 5*time.Second)
	month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := fetcher.FetchMonth(context.Background(), "BTCUSDT", "1m", month)
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("err = %v, want unexpected status 500", err)
	}
}

func TestHTTPFetcherRejectsChecksumMismatch(t *testing.T) {
	zipBytes := makeKlineZip(t, "BTCUSDT-1m-2024-01.csv",
		"1704067200000,1,1,1,1.00000000,1.00000000,1704067259999\n")
	srv := archiveServer(t, zipBytes, true)
	defer srv.Close()

	fetcher := NewHTTPFetcher(srv.URL, 5*time.Second)
	month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := fetcher.FetchMonth(context.Background(), "BTCUSDT", "1m", month)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}
