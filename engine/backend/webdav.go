// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package backend

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WebDAVConfig configures a WebDAV backend.
type WebDAVConfig struct {
	// BaseURL is the WebDAV endpoint, e.g. https://dav.example.com/remote.php/webdav
	BaseURL string
	// Folder is a directory within the endpoint that holds this backend's tree.
	Folder string
	// Username and Password enable HTTP basic authentication when Username is set.
	Username string
	Password string
	// HTTPClient is optional; http.DefaultClient is used when nil.
	HTTPClient *http.Client
	// AllowInsecureHTTP permits credentials over plain http://. Intended only
	// for local test servers; jobs never set it.
	AllowInsecureHTTP bool
}

// WebDAV is a Backend stored on a WebDAV server. The server cannot hardlink,
// so snapshots use the full-copy fallback.
type WebDAV struct {
	root   *url.URL
	cfg    WebDAVConfig
	client *http.Client
	// endpointPath is the URL path of the endpoint; folderSegs are the
	// configured folder split into segments. Ancestors of the folder live above root.
	endpointPath string
	folderSegs   []string

	mu sync.Mutex
	// dirMu serialises collection creation: parallel uploads that create the
	// same directory race, and servers answer the loser with an error.
	dirMu   sync.Mutex
	created map[string]bool // collections known to exist
}

// NewWebDAV returns a WebDAV backend. It does not contact the server.
func NewWebDAV(cfg WebDAVConfig) (*WebDAV, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("webdav: bad URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("webdav: URL must be http or https")
	}
	if u.Scheme == "http" && cfg.Username != "" && !cfg.AllowInsecureHTTP {
		return nil, errors.New("webdav: refusing to send credentials over plain http")
	}
	clean := path.Clean("/" + cfg.Folder)
	endpoint := strings.TrimSuffix(u.Path, "/")
	u.Path = endpoint + clean
	c := cfg.HTTPClient
	if c == nil {
		c = http.DefaultClient
	}
	var segs []string
	if clean != "/" {
		segs = strings.Split(strings.TrimPrefix(clean, "/"), "/")
	}
	return &WebDAV{root: u, cfg: cfg, client: c, created: map[string]bool{}, endpointPath: endpoint, folderSegs: segs}, nil
}

// Capabilities reports that WebDAV cannot hardlink.
func (w *WebDAV) Capabilities() Capabilities { return Capabilities{} }

func (w *WebDAV) urlFor(rel string) string {
	u := *w.root
	if rel != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + rel
	}
	return u.String()
}

func (w *WebDAV) do(ctx context.Context, method, rel string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, w.urlFor(rel), body)
	if err != nil {
		return nil, err
	}
	if w.cfg.Username != "" {
		req.SetBasicAuth(w.cfg.Username, w.cfg.Password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return w.client.Do(req)
}

// drain discards and closes a response body so the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func statusErr(op string, resp *http.Response) error {
	return fmt.Errorf("webdav %s: %s", op, resp.Status)
}

// ensureDirs creates each ancestor collection of rel that does not yet exist.
func (w *WebDAV) ensureDirs(ctx context.Context, rel string) error {
	w.dirMu.Lock()
	defer w.dirMu.Unlock()
	if err := w.ensureFolderAncestors(ctx); err != nil {
		return err
	}
	// The backend's own folder comes first, then each ancestor of rel.
	dirs := []string{""}
	if dir := path.Dir(rel); dir != "." && dir != "" {
		parts := strings.Split(dir, "/")
		for i := range parts {
			dirs = append(dirs, strings.Join(parts[:i+1], "/"))
		}
	}
	for _, cur := range dirs {
		if err := w.mkcol(ctx, cur); err != nil {
			return err
		}
	}
	return nil
}

// mkcol creates one collection (relative path; "" is the backend folder),
// remembering successes so later writes skip the round trip.
func (w *WebDAV) mkcol(ctx context.Context, cur string) error {
	w.mu.Lock()
	known := w.created[cur]
	w.mu.Unlock()
	if known {
		return nil
	}
	resp, err := w.do(ctx, "MKCOL", cur, nil, nil)
	if err != nil {
		return err
	}
	drain(resp)
	// 201 created; 405 means it already exists, which is fine.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
		return statusErr("MKCOL "+cur, resp)
	}
	w.mu.Lock()
	w.created[cur] = true
	w.mu.Unlock()
	return nil
}

// Stat returns size for a file (via HEAD). Modification time is not reliable
// across WebDAV servers, so ModTime is left zero; the engine keeps mtimes in
// snapshot manifests instead.
func (w *WebDAV) Stat(ctx context.Context, rel string) (FileInfo, error) {
	if err := checkRel(rel); err != nil {
		return FileInfo{}, err
	}
	resp, err := w.do(ctx, http.MethodHead, rel, nil, nil)
	if err != nil {
		return FileInfo{}, err
	}
	drain(resp)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return FileInfo{}, ErrNotExist
	case resp.StatusCode/100 != 2:
		return FileInfo{}, statusErr("HEAD", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return FileInfo{Path: rel, Size: size}, nil
}

// Open streams a file.
func (w *WebDAV) Open(ctx context.Context, rel string) (io.ReadCloser, error) {
	if err := checkRel(rel); err != nil {
		return nil, err
	}
	resp, err := w.do(ctx, http.MethodGet, rel, nil, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		drain(resp)
		return nil, ErrNotExist
	case resp.StatusCode/100 != 2:
		drain(resp)
		return nil, statusErr("GET", resp)
	}
	return resp.Body, nil
}

// Write uploads r to rel with PUT. WebDAV PUT replaces the target, which is
// safe here because snapshot files always have new names; the only file
// rewritten in place is the small "latest" marker.
func (w *WebDAV) Write(ctx context.Context, rel string, r io.Reader, _ time.Time) error {
	if err := checkRel(rel); err != nil {
		return err
	}
	if err := w.ensureDirs(ctx, rel); err != nil {
		return err
	}
	resp, err := w.do(ctx, http.MethodPut, rel, r, map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	drain(resp)
	if resp.StatusCode/100 != 2 {
		return statusErr("PUT", resp)
	}
	return nil
}

// Link is unsupported: WebDAV has no hardlinks.
func (w *WebDAV) Link(context.Context, string, string) error { return ErrUnsupported }

// Delete removes one file.
func (w *WebDAV) Delete(ctx context.Context, rel string) error {
	if err := checkRel(rel); err != nil {
		return err
	}
	resp, err := w.do(ctx, http.MethodDelete, rel, nil, nil)
	if err != nil {
		return err
	}
	drain(resp)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotExist
	case resp.StatusCode/100 != 2:
		return statusErr("DELETE", resp)
	}
	return nil
}

// RemoveTree deletes a collection and everything in it. Missing is not an error.
func (w *WebDAV) RemoveTree(ctx context.Context, dir string) error {
	if dir == "" {
		return ErrPathEscape
	}
	if err := checkRel(dir); err != nil {
		return err
	}
	resp, err := w.do(ctx, http.MethodDelete, dir, nil, nil)
	if err != nil {
		return err
	}
	drain(resp)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 2 {
		w.mu.Lock()
		for k := range w.created {
			if k == dir || strings.HasPrefix(k, dir+"/") {
				delete(w.created, k)
			}
		}
		w.mu.Unlock()
		return nil
	}
	return statusErr("DELETE", resp)
}

// Walk visits every file under dir using one PROPFIND per collection
// (Depth: 1), which works on servers that refuse Depth: infinity.
func (w *WebDAV) Walk(ctx context.Context, dir string, fn func(FileInfo) error) error {
	if dir != "" {
		if err := checkRel(dir); err != nil {
			return err
		}
	}
	entries, err := w.propfind(ctx, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		child := e.name
		if dir != "" {
			child = dir + "/" + e.name
		}
		if e.collection {
			if err := w.Walk(ctx, child, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(FileInfo{Path: child, Size: e.size, ModTime: e.modified}); err != nil {
			return err
		}
	}
	return nil
}

// ListDirs returns the sorted names of immediate subcollections of dir.
func (w *WebDAV) ListDirs(ctx context.Context, dir string) ([]string, error) {
	if dir != "" {
		if err := checkRel(dir); err != nil {
			return nil, err
		}
	}
	entries, err := w.propfind(ctx, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.collection {
			names = append(names, e.name)
		}
	}
	sort.Strings(names)
	return names, nil
}

type davEntry struct {
	name       string
	collection bool
	size       int64
	modified   time.Time
}

type multistatus struct {
	Responses []struct {
		Href     string `xml:"DAV: href"`
		Propstat []struct {
			Prop struct {
				Length       string `xml:"DAV: getcontentlength"`
				Modified     string `xml:"DAV: getlastmodified"`
				ResourceType struct {
					Collection *struct{} `xml:"DAV: collection"`
				} `xml:"DAV: resourcetype"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`

// propfind lists the immediate children of dir. A missing dir yields none.
func (w *WebDAV) propfind(ctx context.Context, dir string) ([]davEntry, error) {
	resp, err := w.do(ctx, "PROPFIND", dir, strings.NewReader(propfindBody),
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, statusErr("PROPFIND", resp)
	}
	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("webdav PROPFIND: bad XML: %w", err)
	}
	selfPath := strings.TrimSuffix(w.urlPathFor(dir), "/")
	var out []davEntry
	for _, r := range ms.Responses {
		hu, err := url.Parse(r.Href)
		if err != nil {
			continue
		}
		p := strings.TrimSuffix(hu.Path, "/")
		if p == selfPath || p == "" {
			continue // the collection itself
		}
		name := p[strings.LastIndex(p, "/")+1:]
		e := davEntry{name: name}
		for _, ps := range r.Propstat {
			if ps.Prop.ResourceType.Collection != nil {
				e.collection = true
			}
			e.size, _ = strconv.ParseInt(ps.Prop.Length, 10, 64)
			if t, err := http.ParseTime(ps.Prop.Modified); err == nil {
				e.modified = t
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (w *WebDAV) urlPathFor(rel string) string {
	u := *w.root
	if rel != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + rel
	}
	return u.Path
}

// checkRel rejects paths that are absolute, contain "..", or are otherwise
// unsafe to place in a URL under the backend root.
func checkRel(rel string) error {
	if rel == "" {
		return nil
	}
	if strings.ContainsAny(rel, "\\\x00") || strings.HasPrefix(rel, "/") {
		return ErrPathEscape
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || seg == "" {
			return ErrPathEscape
		}
	}
	return nil
}

// ensureFolderAncestors creates each level of the configured folder above the
// backend root (for Folder "a/b", it creates "a" before "a/b"). Servers reject
// MKCOL when the parent is missing, so this must come first.
func (w *WebDAV) ensureFolderAncestors(ctx context.Context) error {
	w.mu.Lock()
	done := w.created["\x00ancestors"]
	w.mu.Unlock()
	if done {
		return nil
	}
	for i := 1; i < len(w.folderSegs); i++ {
		u := *w.root
		u.Path = w.endpointPath + "/" + strings.Join(w.folderSegs[:i], "/")
		req, err := http.NewRequestWithContext(ctx, "MKCOL", u.String(), nil)
		if err != nil {
			return err
		}
		if w.cfg.Username != "" {
			req.SetBasicAuth(w.cfg.Username, w.cfg.Password)
		}
		resp, err := w.client.Do(req)
		if err != nil {
			return err
		}
		drain(resp)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
			return statusErr("MKCOL "+u.Path, resp)
		}
	}
	w.mu.Lock()
	w.created["\x00ancestors"] = true
	w.mu.Unlock()
	return nil
}
