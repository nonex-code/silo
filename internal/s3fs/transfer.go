package s3fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
)

// Default transfer tuning.
const (
	// SinglePutLimit is the largest file uploaded with one PUT. Larger files
	// use multipart uploads so they can be resumed.
	SinglePutLimit = 32 << 20 // 32 MiB
	// DefaultPartSize is the multipart part size.
	DefaultPartSize = 16 << 20 // 16 MiB
	// DefaultConcurrency is the number of parallel part uploads / downloads.
	DefaultConcurrency = 4
	// uploadStateSuffix is appended to the local path to persist upload state.
	uploadStateSuffix = ".silo-upload.json"
)

// DownloadResult reports what a download did.
type DownloadResult struct {
	Written int64
	Skipped bool
}

// DownloadFile downloads bucket/key into local, resuming a partial file when
// resume is true. progress (optional) receives incremental byte counts.
func (c *Client) DownloadFile(ctx context.Context, bucket, key, local string, resume bool, progress func(int64)) (DownloadResult, error) {
	info, err := c.Stat(ctx, bucket, key)
	if err != nil {
		return DownloadResult{}, err
	}

	var offset int64
	if resume {
		if fi, statErr := os.Stat(local); statErr == nil {
			switch {
			case fi.Size() == info.Size:
				return DownloadResult{Skipped: true}, nil
			case fi.Size() > info.Size:
				offset = 0 // local is longer, restart
			default:
				offset = fi.Size()
			}
		}
	}
	if info.Size == 0 && offset == 0 {
		if _, statErr := os.Stat(local); statErr == nil {
			return DownloadResult{Skipped: true}, nil
		}
	}

	if dir := filepath.Dir(local); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return DownloadResult{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(local, flags, 0o644)
	if err != nil {
		return DownloadResult{}, err
	}
	defer f.Close()

	if offset < info.Size {
		rc, err := c.Get(ctx, bucket, key, offset, 0)
		if err != nil {
			return DownloadResult{Written: offset}, err
		}
		n, err := copyWithProgress(f, rc, func(d int64) {
			if progress != nil {
				progress(d)
			}
		})
		rc.Close()
		offset += n
		if err != nil {
			return DownloadResult{Written: offset}, err
		}
	}
	return DownloadResult{Written: offset}, nil
}

// UploadOptions controls UploadFile.
type UploadOptions struct {
	PartSize    uint64
	Concurrency int
	Resume      bool
	// Progress receives incremental byte counts. For multipart uploads it is
	// updated in part-sized chunks.
	Progress func(int64)
}

// UploadFile uploads a local file to bucket/key. Large files use a resumable
// multipart upload whose state is persisted next to the local file.
func (c *Client) UploadFile(ctx context.Context, bucket, key, local string, opts UploadOptions) error {
	fi, err := os.Stat(local)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", local)
	}

	partSize := opts.PartSize
	if partSize == 0 {
		partSize = DefaultPartSize
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}

	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	size := fi.Size()
	if size <= SinglePutLimit {
		// Small files are uploaded with a single PUT.
		pr := &progressReader{r: f, fn: opts.Progress}
		_, err := c.Client.PutObject(ctx, bucket, key, pr, size, minio.PutObjectOptions{})
		return err
	}
	return c.multipartUpload(ctx, f, bucket, key, local, size, fi.ModTime(), int64(partSize), concurrency, opts.Resume, opts.Progress)
}

// uploadState is persisted next to the file being uploaded.
type uploadState struct {
	Bucket   string `json:"bucket"`
	Key      string `json:"key"`
	UploadID string `json:"uploadId"`
	PartSize int64  `json:"partSize"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"modTimeUnixNano"`
	Local    string `json:"local"`
}

func (s uploadState) matches(bucket, key string, size, partSize int64, mod time.Time) bool {
	return s.Bucket == bucket && s.Key == key && s.Size == size &&
		s.PartSize == partSize && s.ModTime == mod.UnixNano()
}

func (c *Client) multipartUpload(ctx context.Context, f *os.File, bucket, key, local string, size int64, modTime time.Time, partSize int64, concurrency int, resume bool, progress func(int64)) error {
	statePath := local + uploadStateSuffix

	var state *uploadState
	if resume {
		state = readUploadState(statePath)
		if state != nil && !state.matches(bucket, key, size, partSize, modTime) {
			// Stale state: abort the old upload if possible and start over.
			_ = c.Core.AbortMultipartUpload(ctx, state.Bucket, state.Key, state.UploadID)
			state = nil
		}
	} else {
		// Clean up any previous state before starting fresh.
		if old := readUploadState(statePath); old != nil {
			_ = c.Core.AbortMultipartUpload(ctx, old.Bucket, old.Key, old.UploadID)
		}
		_ = os.Remove(statePath)
	}

	partCount := int((size + partSize - 1) / partSize)

	completed := map[int]minio.CompletePart{}
	if state != nil {
		parts, err := c.listParts(ctx, bucket, key, state.UploadID)
		if err == nil {
			for _, p := range parts {
				if p.PartNumber >= 1 && p.PartNumber <= partCount {
					completed[p.PartNumber] = minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
				}
			}
		} else if !IsNotFound(err) {
			return err
		} else {
			// Upload no longer exists on the server, restart.
			state = nil
			completed = map[int]minio.CompletePart{}
		}
	}

	if state == nil {
		uploadID, err := c.Core.NewMultipartUpload(ctx, bucket, key, minio.PutObjectOptions{PartSize: uint64(partSize)})
		if err != nil {
			return err
		}
		state = &uploadState{
			Bucket:   bucket,
			Key:      key,
			UploadID: uploadID,
			PartSize: partSize,
			Size:     size,
			ModTime:  modTime.UnixNano(),
			Local:    local,
		}
		if err := writeUploadState(statePath, state); err != nil {
			// Non fatal: we can still finish this attempt, just no resume.
			_ = err
		}
	}

	// Report progress already accounted for by previously uploaded parts.
	var doneBytes int64
	for n := range completed {
		off := int64(n-1) * partSize
		l := partSize
		if off+l > size {
			l = size - off
		}
		doneBytes += l
	}
	if progress != nil && doneBytes > 0 {
		progress(doneBytes)
	}

	type partJob struct{ num int }
	jobs := make(chan partJob)
	errCh := make(chan error, 1)

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup

	go func() {
		defer close(jobs)
		for i := 1; i <= partCount; i++ {
			if _, ok := completed[i]; ok {
				continue
			}
			select {
			case <-wctx.Done():
				return
			case jobs <- partJob{num: i}:
			}
		}
	}()

	workers := concurrency
	if workers > partCount {
		workers = partCount
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				off := int64(job.num-1) * partSize
				l := partSize
				if off+l > size {
					l = size - off
				}
				sr := io.NewSectionReader(f, off, l)
				p, err := c.Core.PutObjectPart(wctx, bucket, key, state.UploadID, job.num, sr, l, minio.PutObjectPartOptions{
					// Avoid aws-chunked streaming signatures for parts; they are
					// not accepted by every S3 compatible server. The section
					// reader is seekable so minio-go can hash it directly.
					DisableContentSha256: true,
				})
				if err != nil {
					select {
					case errCh <- fmt.Errorf("part %d: %w", job.num, err):
					default:
					}
					cancel()
					return
				}
				mu.Lock()
				completed[p.PartNumber] = minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
				mu.Unlock()
				if progress != nil {
					progress(l)
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var firstErr error
	for err := range errCh {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil && ctx.Err() != nil {
		firstErr = ctx.Err()
	}
	if firstErr != nil {
		return firstErr
	}

	parts := make([]minio.CompletePart, 0, len(completed))
	for _, p := range completed {
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	if _, err := c.Core.CompleteMultipartUpload(ctx, bucket, key, state.UploadID, parts, minio.PutObjectOptions{}); err != nil {
		return err
	}
	_ = os.Remove(statePath)
	return nil
}

func (c *Client) listParts(ctx context.Context, bucket, key, uploadID string) ([]minio.ObjectPart, error) {
	var all []minio.ObjectPart
	marker := 0
	for {
		res, err := c.Core.ListObjectParts(ctx, bucket, key, uploadID, marker, 1000)
		if err != nil {
			return all, err
		}
		all = append(all, res.ObjectParts...)
		if !res.IsTruncated {
			break
		}
		marker = res.NextPartNumberMarker
	}
	return all, nil
}

func readUploadState(path string) *uploadState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s uploadState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func writeUploadState(path string, s *uploadState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

type progressReader struct {
	r  io.Reader
	fn func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.fn != nil {
		p.fn(int64(n))
	}
	return n, err
}

func copyWithProgress(dst io.Writer, src io.Reader, fn func(int64)) (int64, error) {
	buf := make([]byte, 256*1024)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if fn != nil {
				fn(int64(n))
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
