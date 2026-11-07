// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
	"github.com/tpt-solutions/tpt-sync/engine/secrets"
)

// OpenDestination constructs the backend a job writes to. Secrets are read
// from store, keyed by job name; store may be nil for local destinations.
func OpenDestination(job Job, store secrets.Store) (backend.Backend, error) {
	d := job.Destination
	switch d.Type {
	case "local":
		abs, err := filepath.Abs(d.Path)
		if err != nil {
			return nil, err
		}
		return backend.NewLocal(abs)

	case "webdav":
		pw, err := optionalSecret(store, job.Name, d.Username != "")
		if err != nil {
			return nil, err
		}
		return backend.NewWebDAV(backend.WebDAVConfig{
			BaseURL: d.URL, Folder: d.Path, Username: d.Username, Password: pw,
		})

	case "s3":
		sk, err := optionalSecret(store, job.Name, true)
		if err != nil {
			return nil, err
		}
		endpoint, secure, err := parseEndpoint(d.URL)
		if err != nil {
			return nil, err
		}
		if !secure && d.Username != "" {
			return nil, errors.New("refusing to send S3 credentials over plain http; use an https endpoint")
		}
		return backend.NewS3(backend.S3Config{
			Endpoint: endpoint, Secure: secure, Region: d.Region,
			Bucket: d.Bucket, Prefix: d.Path,
			AccessKey: d.Username, SecretKey: sk,
		})
	}
	return nil, fmt.Errorf("unsupported destination type %q", d.Type)
}

// optionalSecret reads the job's password. When required and missing, it
// returns an error that says how to store one.
func optionalSecret(store secrets.Store, jobName string, required bool) (string, error) {
	if !required {
		return "", nil
	}
	if store == nil {
		return "", errors.New("this destination needs a stored password, but no secret store is configured")
	}
	v, err := store.Get(secrets.PasswordAccount(jobName))
	if errors.Is(err, secrets.ErrNotFound) {
		return "", fmt.Errorf("no password stored for job %q; run: tpt-sync secret set %s password", jobName, jobName)
	}
	return v, err
}

// parseEndpoint splits an S3 endpoint URL into host[:port] and a secure flag.
// An empty URL selects AWS S3 over https.
func parseEndpoint(raw string) (host string, secure bool, err error) {
	if raw == "" {
		return "", true, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("bad s3 url: %w", err)
	}
	switch u.Scheme {
	case "https":
		secure = true
	case "http":
		secure = false
	default:
		return "", false, errors.New("s3 url must start with http:// or https://")
	}
	return u.Host, secure, nil
}
