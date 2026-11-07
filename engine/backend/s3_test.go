// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package backend

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// newS3 serves an in-memory S3 over TLS. TLS matters: it is how real S3 is
// reached, and it selects the same request signing path as production.
func newS3(t *testing.T, prefix string) *S3 {
	t.Helper()
	mem := s3mem.New()
	if err := mem.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(gofakes3.New(mem).Server())
	t.Cleanup(ts.Close)
	s, err := NewS3(S3Config{
		Endpoint: strings.TrimPrefix(ts.URL, "https://"), Secure: true,
		Region: "us-east-1", Bucket: "bkt", Prefix: prefix,
		AccessKey: "AK", SecretKey: "SK",
		Transport: ts.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestS3WriteReadStat(t *testing.T) {
	s := newS3(t, "backups/pc1")
	ctx := context.Background()
	if err := s.Write(ctx, "versions/t1/a.bin", strings.NewReader("payload"), time.Now()); err != nil {
		t.Fatal(err)
	}
	fi, err := s.Stat(ctx, "versions/t1/a.bin")
	if err != nil || fi.Size != 7 {
		t.Fatalf("Stat = %+v, %v", fi, err)
	}
	r, err := s.Open(ctx, "versions/t1/a.bin")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "payload" {
		t.Fatalf("read %q", b)
	}
	if _, err := s.Open(ctx, "nope"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("missing open = %v", err)
	}
	if _, err := s.Stat(ctx, "nope"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("missing stat = %v", err)
	}
}

func TestS3WalkListDirsRemove(t *testing.T) {
	s := newS3(t, "")
	ctx := context.Background()
	for _, rel := range []string{"versions/t1/x", "versions/t1/d/y", "versions/t2/z"} {
		if err := s.Write(ctx, rel, strings.NewReader("1"), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := s.ListDirs(ctx, "versions")
	if err != nil || strings.Join(dirs, ",") != "t1,t2" {
		t.Fatalf("ListDirs = %v, %v", dirs, err)
	}
	var walked []string
	if err := s.Walk(ctx, "versions/t1", func(fi FileInfo) error {
		walked = append(walked, fi.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(walked) != 2 {
		t.Fatalf("Walk = %v", walked)
	}
	if err := s.RemoveTree(ctx, "versions/t1"); err != nil {
		t.Fatal(err)
	}
	dirs, _ = s.ListDirs(ctx, "versions")
	if strings.Join(dirs, ",") != "t2" {
		t.Fatalf("after RemoveTree ListDirs = %v", dirs)
	}
	if err := s.Delete(ctx, "versions/t2/z"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "versions/t2/z"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestS3CapabilitiesAndSafety(t *testing.T) {
	s := newS3(t, "")
	ctx := context.Background()
	if s.Capabilities().Hardlink {
		t.Fatal("S3 must not advertise hardlinks")
	}
	if err := s.Link(ctx, "a", "b"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Link = %v", err)
	}
	if err := s.Write(ctx, "../escape", strings.NewReader("x"), time.Now()); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("traversal = %v", err)
	}
	if err := s.RemoveTree(ctx, ""); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("removing whole bucket = %v", err)
	}
}

func TestS3RequiresBucket(t *testing.T) {
	if _, err := NewS3(S3Config{}); err == nil {
		t.Fatal("missing bucket accepted")
	}
}
