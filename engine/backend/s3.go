// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible backend (AWS S3, MinIO, B2 S3 API, …).
type S3Config struct {
	// Endpoint is host[:port], without scheme. Empty uses AWS S3's default.
	Endpoint string
	// Secure selects https. Set false only for local test servers.
	Secure    bool
	Region    string
	Bucket    string
	Prefix    string // folder within the bucket that holds this backend's tree
	AccessKey string
	SecretKey string
	// Transport is optional; nil uses the client default. Tests use it to
	// trust a self-signed test server.
	Transport http.RoundTripper
}

// S3 is a Backend stored in an S3-compatible bucket. Objects cannot be
// hardlinked, so snapshots use the full-copy fallback. S3 has no directories:
// directory operations are emulated with key prefixes.
type S3 struct {
	client *minio.Client
	bucket string
	prefix string // "" or "folder/" with a trailing slash
}

// NewS3 returns an S3 backend. It does not contact the server until used.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "s3.amazonaws.com"
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:    cfg.Secure,
		Region:    cfg.Region,
		Transport: cfg.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	prefix := strings.Trim(path.Clean("/"+cfg.Prefix), "/")
	if prefix != "" {
		prefix += "/"
	}
	return &S3{client: c, bucket: cfg.Bucket, prefix: prefix}, nil
}

// Capabilities reports that S3 cannot hardlink.
func (s *S3) Capabilities() Capabilities { return Capabilities{} }

// key maps a backend-relative path to an object key.
func (s *S3) key(rel string) string { return s.prefix + rel }

// dirPrefix returns the key prefix for a directory, with a trailing slash.
func (s *S3) dirPrefix(dir string) string {
	if dir == "" {
		return s.prefix
	}
	return s.prefix + dir + "/"
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.Code == "NotFound" || resp.StatusCode == 404
}

// Stat returns size for an object.
func (s *S3) Stat(ctx context.Context, rel string) (FileInfo, error) {
	if err := checkRel(rel); err != nil {
		return FileInfo{}, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, s.key(rel), minio.StatObjectOptions{})
	if isNotFound(err) {
		return FileInfo{}, ErrNotExist
	}
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Path: rel, Size: info.Size, ModTime: info.LastModified}, nil
}

// Open streams an object.
func (s *S3) Open(ctx context.Context, rel string) (io.ReadCloser, error) {
	if err := checkRel(rel); err != nil {
		return nil, err
	}
	// GetObject is lazy; StatObject first so a missing key is reported here.
	if _, err := s.client.StatObject(ctx, s.bucket, s.key(rel), minio.StatObjectOptions{}); err != nil {
		if isNotFound(err) {
			return nil, ErrNotExist
		}
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.key(rel), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// Write uploads r as an object. Objects are replaced atomically by S3 on
// completion, so readers never see a partial object.
func (s *S3) Write(ctx context.Context, rel string, r io.Reader, _ time.Time) error {
	if err := checkRel(rel); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, s.bucket, s.key(rel), r, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	return err
}

// Link is unsupported: S3 has no hardlinks.
func (s *S3) Link(context.Context, string, string) error { return ErrUnsupported }

// Delete removes one object.
func (s *S3) Delete(ctx context.Context, rel string) error {
	if _, err := s.Stat(ctx, rel); err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, s.key(rel), minio.RemoveObjectOptions{})
}

// Walk visits every object under dir.
func (s *S3) Walk(ctx context.Context, dir string, fn func(FileInfo) error) error {
	if dir != "" {
		if err := checkRel(dir); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix: s.dirPrefix(dir), Recursive: true,
	}) {
		if obj.Err != nil {
			return obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		rel := strings.TrimPrefix(obj.Key, s.prefix)
		if err := fn(FileInfo{Path: rel, Size: obj.Size, ModTime: obj.LastModified}); err != nil {
			return err
		}
	}
	return nil
}

// ListDirs returns the sorted names of immediate "subdirectories" (common
// prefixes) of dir.
func (s *S3) ListDirs(ctx context.Context, dir string) ([]string, error) {
	if dir != "" {
		if err := checkRel(dir); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var names []string
	dp := s.dirPrefix(dir)
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: dp}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			name := strings.TrimSuffix(strings.TrimPrefix(obj.Key, dp), "/")
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// RemoveTree deletes every object under dir.
func (s *S3) RemoveTree(ctx context.Context, dir string) error {
	if dir == "" {
		return ErrPathEscape
	}
	if err := checkRel(dir); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	objs := make(chan minio.ObjectInfo)
	go func() {
		defer close(objs)
		for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
			Prefix: s.dirPrefix(dir), Recursive: true,
		}) {
			if obj.Err != nil {
				return
			}
			objs <- obj
		}
	}()
	var firstErr error
	for e := range s.client.RemoveObjects(ctx, s.bucket, objs, minio.RemoveObjectsOptions{}) {
		if e.Err != nil && firstErr == nil {
			firstErr = e.Err
		}
	}
	return firstErr
}
