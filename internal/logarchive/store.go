// Package logarchive preserves completed logs in compressed S3 objects.
package logarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

var (
	ErrNotFound   = errors.New("log archive not found")
	ErrInvalidKey = errors.New("invalid log archive key")
)

// Object describes a compressed archive; Key is relative to the configured prefix.
type Object struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// ListResult is one bounded page. NextAfter can be passed to the next List call.
type ListResult struct {
	Objects   []Object `json:"objects"`
	NextAfter string   `json:"next_after,omitempty"`
}

// Store never deletes remote objects. It does not own or remove local files.
type Store struct {
	client    *minio.Client
	transport *http.Transport
	bucket    string
	prefix    string
}

// NewStore constructs a client without contacting S3. Credentials default to AWS
// environment, shared credentials file, then workload/container/instance credentials.
func NewStore(cfg config.LogArchiveConfig) (*Store, error) {
	cfg = cfg.WithDefaults()
	// Validate even when callers construct a store before enabling the worker.
	cfg.Enabled = true
	if errValidate := cfg.Validate(); errValidate != nil {
		return nil, errValidate
	}
	endpoint, _ := url.Parse(cfg.Endpoint)
	secure := endpoint.Scheme == "https"
	transport, errTransport := minio.DefaultTransport(secure)
	if errTransport != nil {
		return nil, errors.New("log archive: initialize transport")
	}
	// Archive traffic has no deadlines after connecting. Cancellation is supplied
	// by the caller's context, including application shutdown and disconnected downloads.
	transport.DialContext = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 0
	transport.IdleConnTimeout = 0
	transport.TLSHandshakeTimeout = 0
	transport.ExpectContinueTimeout = 0
	creds := credentials.NewChainCredentials([]credentials.Provider{
		&credentials.EnvAWS{},
		&credentials.FileAWSCredentials{},
		&credentials.IAM{Client: &http.Client{Timeout: 10 * time.Second}, Region: cfg.Region},
	})
	if cfg.AccessKeyID != "" {
		creds = credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)
	}
	opts := &minio.Options{Creds: creds, Secure: secure, Region: cfg.Region, Transport: transport}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, errClient := minio.New(endpoint.Host, opts)
	if errClient != nil {
		return nil, errors.New("log archive: initialize S3 client")
	}
	return &Store{client: client, transport: transport, bucket: cfg.Bucket, prefix: cfg.Prefix + "/"}, nil
}

// Close releases idle HTTP connections after all streams and operations finish.
func (s *Store) Close() {
	s.transport.CloseIdleConnections()
}

// OriginalName extracts the source filename from a content-addressed archive key.
// Keys contain the SHA256 of the compressed bytes followed by the original name
// with one .gz extension. Existing gzip files keep their compressed name in the
// key; this function returns the underlying .log filename in both cases.
func OriginalName(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 || len(parts[0]) != sha256.Size*2 || strings.ToLower(parts[0]) != parts[0] {
		return "", false
	}
	if _, errDecode := hex.DecodeString(parts[0]); errDecode != nil {
		return "", false
	}
	name := strings.TrimSuffix(parts[1], ".gz")
	if name == parts[1] || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\\\x00\r\n") {
		return "", false
	}
	return name, true
}

func (s *Store) fullKey(key string) (string, error) {
	if _, ok := OriginalName(key); !ok {
		return "", ErrInvalidKey
	}
	return s.prefix + key, nil
}

// UploadVerified uploads a completed gzip file, then reads back the entire object
// and verifies its SHA256 and length. A successful return is the only signal that
// permits the caller to remove its local copy; metadata and ETags are insufficient.
func (s *Store) UploadVerified(ctx context.Context, key, filename string) (err error) {
	fullKey, errKey := s.fullKey(key)
	if errKey != nil {
		return errKey
	}
	file, errOpen := os.Open(filename)
	if errOpen != nil {
		return fmt.Errorf("log archive: open completed log: %w", errOpen)
	}
	defer closeArchiveReader(file)
	hash := sha256.New()
	size, errHash := copyWithContext(ctx, hash, file)
	if errHash != nil {
		return fmt.Errorf("log archive: hash completed log: %w", errHash)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if strings.SplitN(key, "/", 2)[0] != digest {
		return errors.New("log archive: key does not match compressed content digest")
	}
	if _, errSeek := file.Seek(0, io.SeekStart); errSeek != nil {
		return fmt.Errorf("log archive: rewind completed log: %w", errSeek)
	}
	_, errPut := s.client.PutObject(ctx, s.bucket, fullKey, file, size, minio.PutObjectOptions{
		ContentType: "application/gzip", PartSize: 8 * 1024 * 1024, NumThreads: 1,
		UserMetadata: map[string]string{"sha256": digest},
	})
	if errPut != nil {
		return storageError(ctx, "upload", errPut)
	}
	remote, _, errRemote := s.Open(ctx, key)
	if errRemote != nil {
		return errRemote
	}
	hash.Reset()
	remoteSize, errRead := copyWithContext(ctx, hash, remote)
	errClose := remote.Close()
	if errRead != nil {
		return storageError(ctx, "verify content", errRead)
	}
	if errClose != nil {
		return storageError(ctx, "close verification stream", errClose)
	}
	if remoteSize != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("log archive: uploaded content failed SHA256 verification")
	}
	return nil
}

// Stat reads object metadata. It does not verify content integrity.
func (s *Store) Stat(ctx context.Context, key string) (Object, error) {
	fullKey, errKey := s.fullKey(key)
	if errKey != nil {
		return Object{}, errKey
	}
	info, errStat := s.client.StatObject(ctx, s.bucket, fullKey, minio.StatObjectOptions{})
	if errStat != nil {
		return Object{}, storageError(ctx, "stat", errStat)
	}
	return Object{Key: key, Size: info.Size, LastModified: info.LastModified}, nil
}

// Open returns an unchanged compressed stream. The caller must close it.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	fullKey, errKey := s.fullKey(key)
	if errKey != nil {
		return nil, Object{}, errKey
	}
	reader, errGet := s.client.GetObject(ctx, s.bucket, fullKey, minio.GetObjectOptions{})
	if errGet != nil {
		return nil, Object{}, storageError(ctx, "download", errGet)
	}
	info, errStat := reader.Stat()
	if errStat != nil {
		closeArchiveReader(reader)
		return nil, Object{}, storageError(ctx, "download", errStat)
	}
	return reader, Object{Key: key, Size: info.Size, LastModified: info.LastModified}, nil
}

// List returns at most limit objects without loading the whole bucket. It scans
// at most limit+1 entries, so unrelated objects cannot create an unbounded scan.
func (s *Store) List(ctx context.Context, after string, limit int) (ListResult, error) {
	if limit < 1 || limit > 1000 {
		return ListResult{}, errors.New("log archive: list limit must be between 1 and 1000")
	}
	if strings.ContainsAny(after, "\x00\r\n") {
		return ListResult{}, ErrInvalidKey
	}
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	startAfter := ""
	if after != "" {
		startAfter = s.prefix + after
	}
	result := ListResult{Objects: make([]Object, 0, limit)}
	entries := s.client.ListObjects(listCtx, s.bucket, minio.ListObjectsOptions{
		Prefix: s.prefix, Recursive: true, StartAfter: startAfter, MaxKeys: limit,
	})
	count := 0
	lastKey := ""
	for info := range entries {
		if info.Err != nil {
			return ListResult{}, storageError(ctx, "list", info.Err)
		}
		if count == limit {
			result.NextAfter = lastKey
			break
		}
		count++
		lastKey = strings.TrimPrefix(info.Key, s.prefix)
		if _, ok := OriginalName(lastKey); ok {
			result.Objects = append(result.Objects, Object{Key: lastKey, Size: info.Size, LastModified: info.LastModified})
		}
	}
	return result, nil
}

func closeArchiveReader(reader io.Closer) {
	if errClose := reader.Close(); errClose != nil {
		log.Warn("log archive: failed to close stream")
	}
}

// S3 errors can contain server-provided text and signed URLs. Return only known
// codes, never arbitrary upstream messages or credential-bearing request URLs.
func storageError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("log archive: %s: %w", operation, ctx.Err())
	}
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "NoSuchKey", "NoSuchObject", "NotFound":
		return fmt.Errorf("log archive: %s: %w", operation, ErrNotFound)
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "NoSuchBucket":
		return fmt.Errorf("log archive: %s: %s", operation, response.Code)
	default:
		return fmt.Errorf("log archive: %s failed", operation)
	}
}
