// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package davfake is a minimal in-memory WebDAV server for tests. It supports
// the subset tpt-sync uses: MKCOL, PUT, GET, HEAD, DELETE and PROPFIND
// (Depth 0 and 1), with HTTP basic authentication.
package davfake

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server is an in-memory WebDAV tree rooted at Prefix.
type Server struct {
	Prefix   string // URL path under which the tree is served, e.g. "/dav"
	User     string // if non-empty, basic auth is required
	Password string

	mu    sync.Mutex
	files map[string]file // key: slash path relative to the root, no leading slash
	dirs  map[string]bool // collections; "" is the root
}

type file struct {
	data []byte
	mod  time.Time
}

// New returns an empty server served under prefix.
func New(prefix, user, password string) *Server {
	return &Server{
		Prefix: strings.TrimSuffix(prefix, "/"), User: user, Password: password,
		files: map[string]file{}, dirs: map[string]bool{"": true},
	}
}

// Files returns a copy of all stored file paths (for test assertions).
func (s *Server) Files() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.User != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != s.User || p != s.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="davfake"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !strings.HasPrefix(r.URL.Path, s.Prefix+"/") && r.URL.Path != s.Prefix {
		http.NotFound(w, r)
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, s.Prefix), "/")

	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case "MKCOL":
		s.mkcol(w, rel)
	case http.MethodPut:
		s.put(w, r, rel)
	case http.MethodGet, http.MethodHead:
		s.get(w, r, rel)
	case http.MethodDelete:
		s.delete(w, rel)
	case "PROPFIND":
		s.propfind(w, r, rel)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func parentOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func (s *Server) mkcol(w http.ResponseWriter, rel string) {
	if _, isFile := s.files[rel]; s.dirs[rel] || isFile {
		http.Error(w, "exists", http.StatusMethodNotAllowed)
		return
	}
	if !s.dirs[parentOf(rel)] {
		http.Error(w, "parent missing", http.StatusConflict)
		return
	}
	s.dirs[rel] = true
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, rel string) {
	if !s.dirs[parentOf(rel)] {
		http.Error(w, "parent missing", http.StatusConflict)
		return
	}
	if s.dirs[rel] {
		http.Error(w, "is a collection", http.StatusConflict)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, existed := s.files[rel]
	s.files[rel] = file{data: data, mod: time.Now().UTC().Truncate(time.Second)}
	if existed {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, rel string) {
	f, ok := s.files[rel]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(f.data)))
	w.Header().Set("Last-Modified", f.mod.Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(f.data)
}

func (s *Server) delete(w http.ResponseWriter, rel string) {
	if _, ok := s.files[rel]; ok {
		delete(s.files, rel)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.dirs[rel] && rel != "" {
		for k := range s.files {
			if strings.HasPrefix(k, rel+"/") {
				delete(s.files, k)
			}
		}
		for k := range s.dirs {
			if k == rel || strings.HasPrefix(k, rel+"/") {
				delete(s.dirs, k)
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

type msResponse struct {
	XMLName  xml.Name `xml:"DAV: response"`
	Href     string   `xml:"DAV: href"`
	Propstat struct {
		Prop struct {
			Length       *int64  `xml:"DAV: getcontentlength,omitempty"`
			Modified     *string `xml:"DAV: getlastmodified,omitempty"`
			ResourceType struct {
				Collection *struct{} `xml:"DAV: collection,omitempty"`
			} `xml:"DAV: resourcetype"`
		} `xml:"DAV: prop"`
		Status string `xml:"DAV: status"`
	} `xml:"DAV: propstat"`
}

type multistatusOut struct {
	XMLName   xml.Name     `xml:"DAV: multistatus"`
	Responses []msResponse `xml:"DAV: response"`
}

func (s *Server) propfind(w http.ResponseWriter, r *http.Request, rel string) {
	isDir := s.dirs[rel]
	f, isFile := s.files[rel]
	if !isDir && !isFile {
		http.NotFound(w, r)
		return
	}
	var out multistatusOut
	out.Responses = append(out.Responses, s.response(rel, isDir, f))
	if isDir && r.Header.Get("Depth") != "0" {
		var kids []string
		for k := range s.dirs {
			if k != rel && parentOf(k) == rel && k != "" {
				kids = append(kids, k)
			}
		}
		for k := range s.files {
			if parentOf(k) == rel {
				kids = append(kids, k)
			}
		}
		sort.Strings(kids)
		for _, k := range kids {
			if s.dirs[k] {
				out.Responses = append(out.Responses, s.response(k, true, file{}))
			} else {
				out.Responses = append(out.Responses, s.response(k, false, s.files[k]))
			}
		}
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(out)
}

func (s *Server) response(rel string, isDir bool, f file) msResponse {
	href := s.Prefix + "/" + rel
	if isDir && rel != "" {
		href += "/"
	}
	resp := msResponse{Href: (&escapedPath{href}).String()}
	resp.Propstat.Status = "HTTP/1.1 200 OK"
	if isDir {
		resp.Propstat.Prop.ResourceType.Collection = &struct{}{}
	} else {
		n := int64(len(f.data))
		resp.Propstat.Prop.Length = &n
		m := f.mod.Format(http.TimeFormat)
		resp.Propstat.Prop.Modified = &m
	}
	return resp
}

// escapedPath percent-escapes a URL path segment by segment.
type escapedPath struct{ p string }

func (e *escapedPath) String() string {
	segs := strings.Split(e.p, "/")
	for i, s := range segs {
		segs[i] = pathEscape(s)
	}
	return strings.Join(segs, "/")
}

func pathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.ContainsRune("-._~/", rune(c)) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
