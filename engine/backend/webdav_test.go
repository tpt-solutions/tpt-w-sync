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

	"github.com/tpt-solutions/tpt-sync/engine/internal/davfake"
)

func newDAV(t *testing.T, user, pass string) (*WebDAV, *davfake.Server) {
	t.Helper()
	srv := davfake.New("/dav", user, pass)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	w, err := NewWebDAV(WebDAVConfig{
		BaseURL: ts.URL + "/dav", Folder: "backups", Username: user, Password: pass,
		AllowInsecureHTTP: true, // httptest serves plain http
	})
	if err != nil {
		t.Fatal(err)
	}
	return w, srv
}

func TestWebDAVWriteReadStat(t *testing.T) {
	w, srv := newDAV(t, "u", "p")
	ctx := context.Background()
	if err := w.Write(ctx, "snap/a/b.txt", strings.NewReader("hello dav"), time.Now()); err != nil {
		t.Fatal(err)
	}
	r, err := w.Open(ctx, "snap/a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "hello dav" {
		t.Fatalf("read %q", b)
	}
	fi, err := w.Stat(ctx, "snap/a/b.txt")
	if err != nil || fi.Size != 9 {
		t.Fatalf("stat = %+v, %v", fi, err)
	}
	if got := srv.Files(); len(got) != 1 || got[0] != "backups/snap/a/b.txt" {
		t.Fatalf("server files = %v", got)
	}
	if _, err := w.Open(ctx, "missing"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("missing open = %v", err)
	}
	if _, err := w.Stat(ctx, "missing"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("missing stat = %v", err)
	}
}

func TestWebDAVWalkListDirsRemove(t *testing.T) {
	w, _ := newDAV(t, "u", "p")
	ctx := context.Background()
	for _, rel := range []string{"versions/t1/x.txt", "versions/t1/d/y.txt", "versions/t2/z.txt"} {
		if err := w.Write(ctx, rel, strings.NewReader("1"), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := w.ListDirs(ctx, "versions")
	if err != nil || strings.Join(dirs, ",") != "t1,t2" {
		t.Fatalf("ListDirs = %v, %v", dirs, err)
	}
	var walked []string
	if err := w.Walk(ctx, "versions/t1", func(fi FileInfo) error {
		walked = append(walked, fi.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(walked, ",") != "versions/t1/d/y.txt,versions/t1/x.txt" &&
		strings.Join(walked, ",") != "versions/t1/x.txt,versions/t1/d/y.txt" {
		t.Fatalf("Walk = %v", walked)
	}
	if err := w.RemoveTree(ctx, "versions/t1"); err != nil {
		t.Fatal(err)
	}
	dirs, _ = w.ListDirs(ctx, "versions")
	if strings.Join(dirs, ",") != "t2" {
		t.Fatalf("after RemoveTree ListDirs = %v", dirs)
	}
	if err := w.RemoveTree(ctx, "versions/absent"); err != nil {
		t.Fatalf("removing missing tree should succeed: %v", err)
	}
	if err := w.Delete(ctx, "versions/t2/z.txt"); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete(ctx, "versions/t2/z.txt"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestWebDAVNoHardlinksAndSafePaths(t *testing.T) {
	w, _ := newDAV(t, "", "")
	ctx := context.Background()
	if w.Capabilities().Hardlink {
		t.Fatal("WebDAV must not advertise hardlinks")
	}
	if err := w.Link(ctx, "a", "b"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Link = %v", err)
	}
	for _, bad := range []string{"../x", "a/../../x", "/abs", "a\\b", "a//b"} {
		if err := w.Write(ctx, bad, strings.NewReader("x"), time.Now()); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Write(%q) = %v, want ErrPathEscape", bad, err)
		}
	}
}

func TestWebDAVAuthFailure(t *testing.T) {
	srv := davfake.New("/dav", "u", "right")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	w, err := NewWebDAV(WebDAVConfig{BaseURL: ts.URL + "/dav", Username: "u", Password: "wrong", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(context.Background(), "x", strings.NewReader("x"), time.Now()); err == nil {
		t.Fatal("bad credentials accepted")
	}
}

func TestWebDAVRefusesCredentialsOverPlainHTTP(t *testing.T) {
	if _, err := NewWebDAV(WebDAVConfig{BaseURL: "http://example.com/dav", Username: "u", Password: "p"}); err == nil {
		t.Fatal("credentials over http should be refused")
	}
}
