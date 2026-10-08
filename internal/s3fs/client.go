// Package s3fs wraps the MinIO Go SDK with the small set of operations the
// interactive shell needs.
package s3fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nonex-code/silo/internal/config"
)

// Entry describes one object, a common prefix ("directory") or a bucket.
type Entry struct {
	Name            string
	Key             string // full key for objects, prefix (ending in "/") for dirs
	IsDir           bool
	IsBucket        bool
	Size            int64
	LastModified    time.Time
	ETag            string
	ContentType     string
	ContentEncoding string
	StorageClass    string
	Owner           string
	VersionID       string
}

// Client is a thin wrapper around a MinIO client bound to one alias.
type Client struct {
	Alias string
	Cfg   config.Alias
	*mC   // embedded minio client/core bundle
}

// mC bundles the high and low level minio clients.
type mC struct {
	Client *minio.Client
	Core   *minio.Core
}

// New builds a client for an alias. The url may include a scheme; when it does
// not, HTTPS is assumed (matching mc's convention).
func New(name string, a config.Alias) (*Client, error) {
	raw := strings.TrimSpace(a.URL)
	if raw == "" {
		return nil, fmt.Errorf("alias %q has an empty url", name)
	}
	secure := true
	endpoint := raw
	if strings.HasPrefix(raw, "http://") {
		secure = false
		endpoint = strings.TrimPrefix(raw, "http://")
	} else if strings.HasPrefix(raw, "https://") {
		endpoint = strings.TrimPrefix(raw, "https://")
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	var creds *credentials.Credentials
	switch strings.ToLower(a.API) {
	case "s3v2":
		creds = credentials.NewStaticV2(a.AccessKey, a.SecretKey, "")
	default:
		creds = credentials.NewStaticV4(a.AccessKey, a.SecretKey, "")
	}

	lookup := minio.BucketLookupAuto
	switch strings.ToLower(a.Path) {
	case "on", "path", "path-style", "pathstyle":
		lookup = minio.BucketLookupPath
	case "off", "dns", "virtual", "virtual-host", "vhost":
		lookup = minio.BucketLookupDNS
	}
	// mc also stores a "lookup" field on newer versions.
	switch strings.ToLower(a.Lookup) {
	case "path", "on":
		lookup = minio.BucketLookupPath
	case "dns", "off":
		lookup = minio.BucketLookupDNS
	}

	opts := &minio.Options{
		Creds:        creds,
		Secure:       secure,
		BucketLookup: lookup,
		Region:       a.Region,
	}

	cli, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("alias %q: %w", name, err)
	}
	core, err := minio.NewCore(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("alias %q: %w", name, err)
	}
	return &Client{
		Alias: name,
		Cfg:   a,
		mC:    &mC{Client: cli, Core: core},
	}, nil
}

// IsNotFound reports whether an error from the SDK represents a missing bucket
// or object.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound", "NoSuchUpload":
		return true
	}
	return resp.StatusCode == 404
}

// IsNoSuchBucket reports a missing bucket specifically.
func IsNoSuchBucket(err error) bool {
	if err == nil {
		return false
	}
	return minio.ToErrorResponse(err).Code == "NoSuchBucket"
}

// ListBuckets returns all buckets visible to the alias.
func (c *Client) ListBuckets(ctx context.Context) ([]Entry, error) {
	infos, err := c.Client.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, b := range infos {
		entries = append(entries, Entry{
			Name:         b.Name,
			Key:          b.Name,
			IsBucket:     true,
			IsDir:        true,
			LastModified: b.CreationDate,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// List returns the children of prefix inside bucket. When recursive is true the
// whole subtree is returned, otherwise only one level (with common prefixes
// reported as directories).
func (c *Client) List(ctx context.Context, bucket, prefix string, recursive bool) ([]Entry, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: recursive}
	var entries []Entry
	for obj := range c.Client.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return entries, obj.Err
		}
		// The folder marker for the directory being listed has a key equal to
		// the prefix itself; it must not show up as one of its own children.
		if obj.Key == prefix {
			continue
		}
		if strings.HasSuffix(obj.Key, "/") && obj.Size == 0 {
			// Folder marker object or a common prefix reported by the S3 API.
			name := baseName(strings.TrimSuffix(obj.Key, "/"))
			if recursive {
				name = strings.TrimSuffix(strings.TrimPrefix(obj.Key, prefix), "/")
			}
			entries = append(entries, Entry{
				Name:         name,
				Key:          obj.Key,
				IsDir:        true,
				Size:         obj.Size,
				LastModified: obj.LastModified,
				ETag:         obj.ETag,
				StorageClass: obj.StorageClass,
				Owner:        obj.Owner.DisplayName,
			})
			continue
		}
		isDir := !recursive && strings.HasSuffix(obj.Key, "/")
		e := Entry{
			Name:            baseName(strings.TrimSuffix(obj.Key, "/")),
			Key:             obj.Key,
			IsDir:           isDir,
			Size:            obj.Size,
			LastModified:    obj.LastModified,
			ETag:            obj.ETag,
			ContentType:     obj.ContentType,
			ContentEncoding: obj.ContentEncoding,
			StorageClass:    obj.StorageClass,
			Owner:           obj.Owner.DisplayName,
		}
		if recursive {
			e.Name = strings.TrimPrefix(obj.Key, prefix)
		}
		entries = append(entries, e)
	}
	// A folder marker object and a common prefix for the same key can both be
	// reported; keep only one entry per key.
	seen := make(map[string]struct{}, len(entries))
	deduped := entries[:0]
	for _, e := range entries {
		if _, ok := seen[e.Key]; ok {
			continue
		}
		seen[e.Key] = struct{}{}
		deduped = append(deduped, e)
	}
	entries = deduped

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// ListRecursive streams every object under prefix (excluding directory
// markers) to fn. Returning an error stops the walk.
func (c *Client) ListRecursive(ctx context.Context, bucket, prefix string, fn func(Entry) error) error {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: true}
	for obj := range c.Client.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") && obj.Size == 0 {
			continue
		}
		e := Entry{
			Name:            strings.TrimPrefix(obj.Key, prefix),
			Key:             obj.Key,
			Size:            obj.Size,
			LastModified:    obj.LastModified,
			ETag:            obj.ETag,
			ContentType:     obj.ContentType,
			ContentEncoding: obj.ContentEncoding,
			StorageClass:    obj.StorageClass,
			Owner:           obj.Owner.DisplayName,
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// ListKeys returns every key under prefix, including zero-byte folder
// markers. It is used by destructive operations so that prefixes are removed
// completely rather than leaving empty directory markers behind.
func (c *Client) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: true}
	var keys []string
	for obj := range c.Client.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return keys, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// Stat returns information about a single object.
func (c *Client) Stat(ctx context.Context, bucket, key string) (Entry, error) {
	info, err := c.Client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Name:            baseName(key),
		Key:             info.Key,
		Size:            info.Size,
		LastModified:    info.LastModified,
		ETag:            info.ETag,
		ContentType:     info.ContentType,
		ContentEncoding: info.ContentEncoding,
		StorageClass:    info.StorageClass,
		Owner:           info.Owner.DisplayName,
		VersionID:       info.VersionID,
	}, nil
}

// Get returns a reader for an object. When start > 0 a byte range is
// requested; length <= 0 means "to the end".
func (c *Client) Get(ctx context.Context, bucket, key string, start, length int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if start > 0 || length > 0 {
		end := int64(0)
		if length > 0 {
			end = start + length - 1
		}
		if err := opts.SetRange(start, end); err != nil {
			return nil, err
		}
	}
	obj, err := c.Client.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// GetRange returns a reader for [start, end] inclusive.
func (c *Client) GetRange(ctx context.Context, bucket, key string, start, end int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(start, end); err != nil {
		return nil, err
	}
	obj, err := c.Client.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// GetTail returns up to n bytes from the end of an object.
func (c *Client) GetTail(ctx context.Context, bucket, key string, n int64) ([]byte, error) {
	info, err := c.Stat(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > info.Size {
		n = info.Size
	}
	if info.Size == 0 {
		return nil, nil
	}
	start := info.Size - n
	rc, err := c.GetRange(ctx, bucket, key, start, info.Size-1)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Copy performs a server side copy of a single object within the same alias.
func (c *Client) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	_, err := c.Client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
		minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey},
	)
	return err
}

// RemoveObject deletes one object.
func (c *Client) RemoveObject(ctx context.Context, bucket, key string) error {
	return c.Client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

// RemoveObjects deletes many objects using the batch API.
func (c *Client) RemoveObjects(ctx context.Context, bucket string, keys []string, onErr func(key string, err error)) error {
	if len(keys) == 0 {
		return nil
	}
	ch := make(chan minio.ObjectInfo, 1)
	go func() {
		defer close(ch)
		for _, k := range keys {
			select {
			case <-ctx.Done():
				return
			case ch <- minio.ObjectInfo{Key: k}:
			}
		}
	}()
	for err := range c.Client.RemoveObjects(ctx, bucket, ch, minio.RemoveObjectsOptions{}) {
		if onErr != nil {
			onErr(err.ObjectName, err.Err)
		}
	}
	return nil
}

// MakeBucket creates a bucket.
func (c *Client) MakeBucket(ctx context.Context, bucket string) error {
	return c.Client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: c.Cfg.Region})
}

// RemoveBucket removes an empty bucket.
func (c *Client) RemoveBucket(ctx context.Context, bucket string) error {
	return c.Client.RemoveBucket(ctx, bucket)
}

// BucketExists reports whether a bucket exists.
func (c *Client) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return c.Client.BucketExists(ctx, bucket)
}

// PresignedGet returns a presigned download URL.
func (c *Client) PresignedGet(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	u, err := c.Client.PresignedGetObject(ctx, bucket, key, expires, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PutReader uploads data of a known size using a single PUT. It disables the
// streaming (aws-chunked) signature so that zero length objects are sent with
// an explicit Content-Length: 0, which every S3 implementation accepts.
func (c *Client) PutReader(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.Client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{
		ContentType:          contentType,
		DisableContentSha256: true,
	})
	return err
}

// PutBytes uploads an in-memory blob.
func (c *Client) PutBytes(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	return c.PutReader(ctx, bucket, key, bytes.NewReader(data), int64(len(data)), contentType)
}

// Touch creates an empty placeholder object.
func (c *Client) Touch(ctx context.Context, bucket, key string) error {
	return c.PutBytes(ctx, bucket, key, nil, "application/octet-stream")
}

// baseName returns the last path component.
func baseName(key string) string {
	key = strings.TrimSuffix(key, "/")
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// ErrNotObject is returned when an operation expects an object but receives a
// directory or alias.
var ErrNotObject = errors.New("not an object")
