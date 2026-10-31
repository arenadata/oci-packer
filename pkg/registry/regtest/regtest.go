/*
  Copyright (c) 2026 Arenadata Softwer LLC.
  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

// Package regtest is a registry for tests: it serves what a resolver holds — an OCI layout, say — over the
// distribution API, read-only: the manifests of one repository by tag or digest, and the blobs. A test of a client
// (or of a tool built on one) packs into a layout, serves it, and reads it back over HTTP, with no registry
// behind it.
package regtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

// Serve starts a registry over src that holds the repository repo. The server is closed with the test; its
// address, without the scheme, is Host, and a client takes it with plain HTTP.
func Serve(t testing.TB, src registry.Resolver, repo string) *httptest.Server {
	t.Helper()
	prefix := "/v2/" + strings.Trim(repo, "/") + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasPrefix(rest, "manifests/"):
			serveManifest(w, r, src, repo, strings.TrimPrefix(rest, "manifests/"))
		case strings.HasPrefix(rest, "blobs/"):
			serveBlob(w, r, src, strings.TrimPrefix(rest, "blobs/"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Host is the address of a server, as an oci:// reference names it.
func Host(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func serveManifest(w http.ResponseWriter, r *http.Request, src registry.Resolver, repo, ref string) {
	if d, err := digest.Parse(ref); err == nil {
		// a manifest by its digest is content like any other
		body, err := src.Fetch(r.Context(), reference.Reference{Ref: d.String()})
		if err != nil {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var head struct {
			MediaType string `json:"mediaType"`
		}
		_ = json.Unmarshal(raw, &head)
		if len(head.MediaType) == 0 {
			head.MediaType = ocispecv1.MediaTypeImageManifest
		}
		w.Header().Set("Content-Type", head.MediaType)
		w.Header().Set("Docker-Content-Digest", d.String())
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(raw)
		}
		return
	}
	desc, err := src.Resolve(r.Context(), reference.Reference{Ref: strings.Trim(repo, "/") + ":" + ref}) // a layout names a tag <repo>:<tag>
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", desc.MediaType)
	w.Header().Set("Docker-Content-Digest", desc.Digest.String())
	w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
	if r.Method == http.MethodHead {
		return
	}
	body, err := src.Fetch(r.Context(), reference.Reference{Ref: desc.Digest.String()}.WithDescriptor(desc))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = body.Close() }()
	_, _ = io.Copy(w, body)
}

func serveBlob(w http.ResponseWriter, r *http.Request, src registry.Resolver, dgst string) {
	d, err := digest.Parse(dgst)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := src.Fetch(r.Context(), reference.Reference{Ref: d.String()})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Docker-Content-Digest", d.String())
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, body)
}
