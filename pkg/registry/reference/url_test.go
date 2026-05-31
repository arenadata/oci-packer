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

package reference

import (
	"net/url"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
)

// ---------------------------------------------------------------------------
// Reference.URL
// ---------------------------------------------------------------------------

func TestReference_URL_HTTPS(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)

	if urlObj.scheme != "https" {
		t.Errorf("Expected https scheme, got %s", urlObj.scheme)
	}
	// Note: docker.io gets normalized to index.docker.io
	if urlObj.ref.Host != "index.docker.io" {
		t.Errorf("Expected host index.docker.io (normalized from docker.io), got %s", urlObj.ref.Host)
	}
}

func TestReference_URL_HTTP(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "localhost:5000",
		Path:   "myapp",
		Ref:    "v1.0",
	}

	urlObj := ref.URL(true)

	if urlObj.scheme != "http" {
		t.Errorf("Expected http scheme, got %s", urlObj.scheme)
	}
}

func TestReference_URL_DockerIONormalization(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"docker.io", "docker.io", "index.docker.io"},
		{"registry-1.docker.io", "registry-1.docker.io", "index.docker.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := Reference{
				Scheme: RegistryScheme,
				Host:   tt.input,
				Path:   "nginx",
				Ref:    "latest",
			}

			urlObj := ref.URL(false)
			if urlObj.ref.Host != tt.expect {
				t.Errorf("Expected host %s, got %s", tt.expect, urlObj.ref.Host)
			}
		})
	}
}

func TestReference_URL_DockerIOLibraryPrefix(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)

	if urlObj.ref.Path != "library/nginx" {
		t.Errorf("Expected path library/nginx, got %s", urlObj.ref.Path)
	}
}

func TestReference_URL_DockerIOPreservesNestedPath(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "myrepo/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)

	if urlObj.ref.Path != "myrepo/nginx" {
		t.Errorf("Expected path myrepo/nginx, got %s (should preserve multi-part paths)", urlObj.ref.Path)
	}
}

// ---------------------------------------------------------------------------
// Url.Path
// ---------------------------------------------------------------------------

func TestUrl_Path(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)
	path := urlObj.Path()

	if path != "library/nginx" {
		t.Errorf("Path() = %q, want library/nginx", path)
	}
}

func TestUrl_Path_SingleLevel(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.io",
		Path:   "myapp",
		Ref:    "v1",
	}

	urlObj := ref.URL(false)
	path := urlObj.Path()

	if path != "myapp" {
		t.Errorf("Path() = %q, want myapp", path)
	}
}

// ---------------------------------------------------------------------------
// Url.Manifests
// ---------------------------------------------------------------------------

func TestUrl_Manifests_WithTag(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)
	manifestsURL := urlObj.Manifests()

	// Verify exact URL structure, not just substring presence.
	parsed, err := url.Parse(manifestsURL)
	if err != nil {
		t.Fatalf("Manifests() returned unparseable URL %q: %v", manifestsURL, err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("scheme = %q, want https", parsed.Scheme)
	}
	if !strings.HasSuffix(parsed.Host, "docker.io") {
		t.Errorf("host = %q, want *docker.io", parsed.Host)
	}
	wantPath := "/v2/library/nginx/manifests/latest"
	if parsed.Path != wantPath {
		t.Errorf("path = %q, want %q", parsed.Path, wantPath)
	}
}

func TestUrl_Manifests_WithDigest(t *testing.T) {
	dgst := "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b"
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.example.com",
		Path:   "team/project",
		Ref:    dgst,
	}

	urlObj := ref.URL(false)
	manifestsURL := urlObj.Manifests()

	parsed, err := url.Parse(manifestsURL)
	if err != nil {
		t.Fatalf("Manifests() returned unparseable URL %q: %v", manifestsURL, err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("scheme = %q, want https", parsed.Scheme)
	}
	wantPath := "/v2/team/project/manifests/" + dgst
	if parsed.Path != wantPath {
		t.Errorf("path = %q, want %q", parsed.Path, wantPath)
	}
}

func TestUrl_Manifests_LocalRegistry(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "localhost:5000",
		Path:   "myapp",
		Ref:    "v1.0",
	}

	urlObj := ref.URL(true)
	manifestsURL := urlObj.Manifests()

	if !strings.Contains(manifestsURL, "http://") {
		t.Errorf("Expected http scheme in URL")
	}
	if !strings.Contains(manifestsURL, "localhost:5000") {
		t.Errorf("Expected localhost:5000 in URL")
	}
	if !strings.Contains(manifestsURL, "/manifests/v1.0") {
		t.Errorf("Expected /manifests/v1.0 in URL")
	}
}

// ---------------------------------------------------------------------------
// Url.Blobs
// ---------------------------------------------------------------------------

func TestUrl_Blobs_WithTag(t *testing.T) {
	// Blobs() returns the blobs endpoint for the repository; the Ref is appended
	// as-is (tag or digest). Verify the exact path structure.
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)
	blobsURL := urlObj.Blobs()

	parsed, err := url.Parse(blobsURL)
	if err != nil {
		t.Fatalf("Blobs() returned unparseable URL %q: %v", blobsURL, err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("scheme = %q, want https", parsed.Scheme)
	}
	if !strings.HasPrefix(parsed.Path, "/v2/library/nginx/blobs/") {
		t.Errorf("path = %q, want prefix /v2/library/nginx/blobs/", parsed.Path)
	}
}

func TestUrl_Blobs_WithDigest(t *testing.T) {
	dgst := "sha256:abc123def456"
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.io",
		Path:   "app",
		Ref:    dgst,
	}

	urlObj := ref.URL(false)
	blobsURL := urlObj.Blobs()

	if !strings.Contains(blobsURL, "/blobs/") {
		t.Errorf("Expected /blobs/ in URL")
	}
	if !strings.Contains(blobsURL, dgst) {
		t.Errorf("Expected digest %s in URL", dgst)
	}
}

// ---------------------------------------------------------------------------
// Url.Uploads / Url.UploadsUrl
// ---------------------------------------------------------------------------

func TestUrl_Uploads(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)
	uploadsURL := urlObj.Uploads()

	if !strings.Contains(uploadsURL, "https://") {
		t.Errorf("Expected https scheme in URL")
	}
	if !strings.Contains(uploadsURL, "/v2/") {
		t.Errorf("Expected /v2/ in URL")
	}
	if !strings.Contains(uploadsURL, "/blobs/uploads/") {
		t.Errorf("Expected /blobs/uploads/ in URL")
	}
}

func TestUrl_UploadsUrl(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "localhost:5000",
		Path:   "myapp",
		Ref:    "v1",
	}

	urlObj := ref.URL(true)
	uploadsURL := urlObj.UploadsUrl()

	if uploadsURL == nil {
		t.Fatalf("UploadsUrl should not return nil")
	}

	urlStr := uploadsURL.String()
	if !strings.Contains(urlStr, "http://") {
		t.Errorf("Expected http scheme in URL")
	}
	if !strings.HasSuffix(uploadsURL.Path, "/") {
		t.Errorf("Expected UploadsUrl path to end with /")
	}
}

func TestUrl_UploadsUrl_PathStructure(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.example.com",
		Path:   "team/project/app",
		Ref:    "v2",
	}

	urlObj := ref.URL(false)
	uploadsURL := urlObj.UploadsUrl()

	expectedPath := "/v2/team/project/app/blobs/uploads/"
	if uploadsURL.Path != expectedPath {
		t.Errorf("UploadsUrl path = %q, want %q", uploadsURL.Path, expectedPath)
	}
}

// ---------------------------------------------------------------------------
// Url.Mount
// ---------------------------------------------------------------------------

func TestUrl_Mount_BasicValues(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	urlObj := ref.URL(false)

	mountDigest := digest.Digest("sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b")
	fromRef := "docker.io/library/ubuntu:20.04"

	mountURL := urlObj.Mount(mountDigest, fromRef)

	if !strings.Contains(mountURL, "https://") {
		t.Errorf("Expected https scheme in mount URL")
	}
	if !strings.Contains(mountURL, "/blobs/uploads/") {
		t.Errorf("Expected /blobs/uploads/ in mount URL")
	}
	if !strings.Contains(mountURL, "mount=") {
		t.Errorf("Expected mount parameter in query string")
	}
	if !strings.Contains(mountURL, "from=") {
		t.Errorf("Expected from parameter in query string")
	}
	if !strings.Contains(mountURL, url.QueryEscape(mountDigest.String())) {
		t.Errorf("Expected mount digest in query string")
	}
	if !strings.Contains(mountURL, url.QueryEscape(fromRef)) {
		t.Errorf("Expected from ref in query string")
	}
}

func TestUrl_Mount_QueryEncoding(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.example.com",
		Path:   "app",
		Ref:    "v1",
	}

	urlObj := ref.URL(true)

	mountDigest := digest.Digest("sha256:1234567890abcdef")
	fromRef := "registry.example.com/base/image:tag"

	mountURL := urlObj.Mount(mountDigest, fromRef)

	// Verify the URL is properly formatted with query parameters
	parsedURL, err := url.Parse(mountURL)
	if err != nil {
		t.Fatalf("Failed to parse mount URL: %v", err)
	}

	query := parsedURL.Query()
	if query.Get("mount") != mountDigest.String() {
		t.Errorf("Mount query param = %q, want %q", query.Get("mount"), mountDigest.String())
	}
	if query.Get("from") != fromRef {
		t.Errorf("From query param = %q, want %q", query.Get("from"), fromRef)
	}
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestUrl_CompleteFlow(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "registry.example.com:5000",
		Path:   "team/project/service",
		Ref:    "v1.2.3",
	}

	urlObj := ref.URL(true)

	// All methods should work together
	if urlObj.Path() != "team/project/service" {
		t.Errorf("Path should return correct path")
	}

	manifestsURL := urlObj.Manifests()
	if !strings.Contains(manifestsURL, "http://registry.example.com:5000") {
		t.Errorf("Manifests should contain correct host and port")
	}

	blobsURL := urlObj.Blobs()
	if !strings.Contains(blobsURL, "/blobs/v1.2.3") {
		t.Errorf("Blobs should contain blob path with tag")
	}

	uploadsURL := urlObj.Uploads()
	if !strings.Contains(uploadsURL, "/blobs/uploads/") {
		t.Errorf("Uploads should contain upload path")
	}
}

func TestUrl_SchemeConsistency(t *testing.T) {
	tests := []struct {
		name      string
		plainHttp bool
		expect    string
	}{
		{"https", false, "https"},
		{"http", true, "http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := Reference{
				Scheme: RegistryScheme,
				Host:   "example.com",
				Path:   "image",
				Ref:    "tag",
			}

			urlObj := ref.URL(tt.plainHttp)

			for _, urlStr := range []string{
				urlObj.Manifests(),
				urlObj.Blobs(),
				urlObj.Uploads(),
			} {
				if !strings.Contains(urlStr, tt.expect+"://") {
					t.Errorf("%s URL should start with %s://", tt.name, tt.expect)
				}
			}
		})
	}
}
