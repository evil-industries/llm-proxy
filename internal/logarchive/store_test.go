package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func testCompressedLog(t *testing.T) ([]byte, string, string) {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, errWrite := writer.Write([]byte("request and response payload preserved verbatim\n")); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	data := compressed.Bytes()
	digest := sha256.Sum256(data)
	key := hex.EncodeToString(digest[:]) + "/request-123.log.gz"
	filename := filepath.Join(t.TempDir(), "completed.gz")
	if errWrite := os.WriteFile(filename, data, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	return data, key, filename
}

func testStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, errStore := NewStore(config.LogArchiveConfig{
		Endpoint: server.URL, Bucket: "archive-test", Region: "us-east-1", Prefix: "deployment/logs",
		AccessKeyID: "test-access", SecretAccessKey: "test-secret", PathStyle: true,
	})
	if errStore != nil {
		t.Fatal(errStore)
	}
	t.Cleanup(store.Close)
	return store
}

func TestUploadVerifiedReadsActualRemoteContent(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%v", corrupt), func(t *testing.T) {
			data, key, filename := testCompressedLog(t)
			var mu sync.Mutex
			var uploaded []byte
			var downloads int
			store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path != "/archive-test/deployment/logs/"+key {
					t.Errorf("unexpected object path: %s", r.URL.Path)
				}
				w.Header().Set("ETag", `"opaque-etag"`)
				switch r.Method {
				case http.MethodPut:
					var errRead error
					var body io.Reader = r.Body
					if strings.Contains(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-AWS4-HMAC-SHA256") {
						body = httputil.NewChunkedReader(body)
					}
					uploaded, errRead = io.ReadAll(body)
					if errRead != nil {
						t.Error(errRead)
					}
				case http.MethodGet, http.MethodHead:
					body := append([]byte(nil), data...)
					if corrupt {
						body[0] ^= 1 // Same length and metadata, different actual bytes.
					}
					w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
					w.Header().Set("Content-Type", "application/gzip")
					w.Header().Set("X-Amz-Meta-Sha256", strings.Split(key, "/")[0])
					if r.Method == http.MethodGet {
						downloads++
						_, _ = w.Write(body)
					}
				default:
					t.Errorf("unexpected method %s", r.Method)
				}
			})
			errUpload := store.UploadVerified(context.Background(), key, filename)
			if corrupt && (errUpload == nil || !strings.Contains(errUpload.Error(), "SHA256")) {
				t.Fatalf("corrupt upload error = %v", errUpload)
			}
			if !corrupt && errUpload != nil {
				t.Fatal(errUpload)
			}
			mu.Lock()
			defer mu.Unlock()
			if downloads == 0 || !bytes.Equal(uploaded, data) {
				t.Fatalf("downloads=%d; uploaded gzip differs: %v", downloads, !bytes.Equal(uploaded, data))
			}
			if remaining, errRead := os.ReadFile(filename); errRead != nil || !bytes.Equal(remaining, data) {
				t.Fatalf("store modified the source: %v", errRead)
			}
		})
	}
}

func TestUploadRejectsDigestMismatchWithoutNetwork(t *testing.T) {
	_, _, filename := testCompressedLog(t)
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") })
	errUpload := store.UploadVerified(context.Background(), strings.Repeat("0", 64)+"/request.log.gz", filename)
	if errUpload == nil || !strings.Contains(errUpload.Error(), "digest") {
		t.Fatalf("error=%v", errUpload)
	}
}

func TestArchiveListPaginationAndPrefixIsolation(t *testing.T) {
	key1 := strings.Repeat("1", 64) + "/first.log.gz"
	key2 := strings.Repeat("2", 64) + "/second.log.gz"
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("prefix") != "deployment/logs/" || query.Get("max-keys") != "1" {
			t.Errorf("unbounded or wrong-prefix request: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/xml")
		if query.Get("start-after") == "deployment/logs/"+key1 || query.Get("continuation-token") == "page2" {
			_, _ = fmt.Fprintf(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>deployment/logs/%s</Key><Size>22</Size><LastModified>2026-01-01T00:00:00Z</LastModified></Contents></ListBucketResult>`, key2)
			return
		}
		_, _ = fmt.Fprintf(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>page2</NextContinuationToken><Contents><Key>deployment/logs/%s</Key><Size>11</Size><LastModified>2026-01-01T00:00:00Z</LastModified></Contents></ListBucketResult>`, key1)
	})
	page, errList := store.List(context.Background(), "", 1)
	if errList != nil || len(page.Objects) != 1 || page.Objects[0].Key != key1 || page.NextAfter != key1 {
		t.Fatalf("first page=%+v error=%v", page, errList)
	}
	page, errList = store.List(context.Background(), page.NextAfter, 1)
	if errList != nil || len(page.Objects) != 1 || page.Objects[0].Key != key2 || page.NextAfter != "" {
		t.Fatalf("second page=%+v error=%v", page, errList)
	}
}

func TestArchiveMissingObjectAndSecretSafeErrors(t *testing.T) {
	key := strings.Repeat("a", 64) + "/request.log.gz"
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>credential-secret-echo</Message></Error>`)
	})
	reader, _, errOpen := store.Open(context.Background(), key)
	if reader != nil || !errors.Is(errOpen, ErrNotFound) || strings.Contains(errOpen.Error(), "credential-secret") {
		t.Fatalf("reader=%v error=%v", reader, errOpen)
	}
}

func TestUploadVerificationReadFailurePreservesLocalFile(t *testing.T) {
	data, key, filename := testCompressedLog(t)
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"opaque-etag"`)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>credential-secret-echo</Message></Error>`)
	})
	errUpload := store.UploadVerified(context.Background(), key, filename)
	if errUpload == nil || !strings.Contains(errUpload.Error(), "AccessDenied") || strings.Contains(errUpload.Error(), "credential-secret") {
		t.Fatalf("error=%v", errUpload)
	}
	if remaining, errRead := os.ReadFile(filename); errRead != nil || !bytes.Equal(remaining, data) {
		t.Fatalf("store modified the source: %v", errRead)
	}
}

func TestArchiveCanceledContext(t *testing.T) {
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, errList := store.List(ctx, "", 1)
	if !errors.Is(errList, context.Canceled) {
		t.Fatalf("error=%v", errList)
	}
}

func TestUploadCanceledBeforeHash(t *testing.T) {
	_, key, filename := testCompressedLog(t)
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if errUpload := store.UploadVerified(ctx, key, filename); !errors.Is(errUpload, context.Canceled) {
		t.Fatalf("error=%v", errUpload)
	}
}

func TestCopyWithContextCanceledLocalFile(t *testing.T) {
	data, _, filename := testCompressedLog(t)
	file, errOpen := os.Open(filename)
	if errOpen != nil {
		t.Fatal(errOpen)
	}
	defer closeArchiveReader(file)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var dst bytes.Buffer
	if copied, errCopy := copyWithContext(ctx, &dst, file); copied != 0 || !errors.Is(errCopy, context.Canceled) {
		t.Fatalf("copied=%d error=%v (file contains %d bytes)", copied, errCopy, len(data))
	}
	if dst.Len() != 0 {
		t.Fatal("canceled copy wrote file contents")
	}
}

func TestCopyWithContextStopsBetweenReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterRead{cancel: cancel}
	copied, errCopy := copyWithContext(ctx, io.Discard, reader)
	if copied != 1 || !errors.Is(errCopy, context.Canceled) || reader.reads != 1 {
		t.Fatalf("copied=%d reads=%d error=%v", copied, reader.reads, errCopy)
	}
}

type cancelAfterRead struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelAfterRead) Read(buffer []byte) (int, error) {
	r.reads++
	buffer[0] = 'a'
	r.cancel()
	return 1, nil
}

func (r *cancelAfterRead) WriteTo(io.Writer) (int64, error) {
	return 0, errors.New("WriterTo must not bypass context checks")
}

func TestOriginalNameRejectsUnsafeKeys(t *testing.T) {
	validPrefix := strings.Repeat("a", 64) + "/"
	for _, key := range []string{"../request.log.gz", validPrefix + "../request.log.gz", validPrefix + ".gz", validPrefix + "../.gz", validPrefix + "request\\.log.gz", validPrefix + "request\n.log.gz"} {
		if _, ok := OriginalName(key); ok {
			t.Errorf("accepted unsafe key %q", key)
		}
	}
	if name, ok := OriginalName(validPrefix + "request.log.gz"); !ok || name != "request.log" {
		t.Fatalf("name=%q ok=%v", name, ok)
	}
}
