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

package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// MockResolver implements registry.Resolver for testing
type MockResolver struct {
	fetchFunc func(context.Context, reference.Reference) (io.ReadCloser, error)
}

func (m *MockResolver) Fetch(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
	if m.fetchFunc != nil {
		return m.fetchFunc(ctx, ref)
	}
	return io.NopCloser(bytes.NewReader([]byte("mock content"))), nil
}

func (m *MockResolver) Resolve(context.Context, reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}

func (m *MockResolver) Exists(context.Context, reference.Reference) (bool, error) {
	return true, nil
}

func (m *MockResolver) FetchReference(context.Context, reference.Reference) (ocispecv1.Descriptor, io.ReadCloser, error) {
	return ocispecv1.Descriptor{}, nil, nil
}

func (m *MockResolver) MountFrom(context.Context, reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}

func (m *MockResolver) Push(context.Context, ocispecv1.Descriptor, io.Reader) error {
	return nil
}

func (m *MockResolver) SetTag(context.Context, ocispecv1.Descriptor) error {
	return nil
}

// MockCache implements Cache for testing
type MockCache struct {
	data map[string]ocispecv1.Descriptor
}

func NewMockCache() *MockCache {
	return &MockCache{data: make(map[string]ocispecv1.Descriptor)}
}

func (m *MockCache) Set(repoUrl, platform, title string, desc ocispecv1.Descriptor) {
	key := repoUrl + "|" + platform + "|" + title
	m.data[key] = desc
}

func (m *MockCache) Get(repoUrl, platform, title string) ocispecv1.Descriptor {
	key := repoUrl + "|" + platform + "|" + title
	return m.data[key]
}

func (m *MockCache) Del(repoUrl, platform, title string) {
	key := repoUrl + "|" + platform + "|" + title
	delete(m.data, key)
}

func TestProxyServeHTTPGetMethod(t *testing.T) {
	mockResolver := &MockResolver{
		fetchFunc: func(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("test content"))), nil
		},
	}

	mockCache := NewMockCache()
	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      12,
		Digest:    "sha256:test",
		Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "testimage",
		},
	}
	mockCache.Set("oci:///path", "", "testimage", descriptor)

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			return []ocispecv1.Descriptor{descriptor}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/path/reference?title=testimage", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	if string(body) != "test content" {
		t.Errorf("Expected body 'test content', got %s", string(body))
	}
}

func TestProxyServeHTTPHeadMethod(t *testing.T) {
	mockResolver := &MockResolver{}
	mockCache := NewMockCache()
	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      100,
		Digest:    "sha256:test",
		Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "testimage",
		},
	}
	mockCache.Set("oci:///path", "", "testimage", descriptor)

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			return []ocispecv1.Descriptor{descriptor}, nil
		},
	}

	req := httptest.NewRequest(http.MethodHead, "/path/reference?title=testimage", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestProxyServeHTTPMethodNotAllowed(t *testing.T) {
	proxy := &Proxy{
		resolver: &MockResolver{},
		cache:    NewMockCache(),
		conv:     defaultConverter,
	}

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/path/reference?title=test", nil)
			w := httptest.NewRecorder()

			proxy.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405, got %d", w.Code)
			}
		})
	}
}

func TestProxyServeHTTPInvalidRequest(t *testing.T) {
	proxy := &Proxy{
		resolver: &MockResolver{},
		cache:    NewMockCache(),
		conv:     defaultConverter,
	}

	// Missing title parameter
	req := httptest.NewRequest(http.MethodGet, "/path/reference", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestProxyServeHTTPNotFound(t *testing.T) {
	mockResolver := &MockResolver{}
	mockCache := NewMockCache()

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			// Return descriptors without matching title
			return []ocispecv1.Descriptor{
				{
					Digest: "sha256:other",
					Annotations: map[string]string{
						ocispecv1.AnnotationTitle: "otherimage",
					},
				},
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/path/reference?title=notfound", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestWithReferenceConverter(t *testing.T) {
	customConverter := func(r *http.Request) (reference.Reference, map[string]string, error) {
		return reference.Reference{}, map[string]string{}, nil
	}

	proxy := &Proxy{}
	opt := WithReferenceConverter(customConverter)
	opt(proxy)

	// We can't directly compare functions, but we can verify the proxy was modified
	if proxy.conv == nil {
		t.Error("Expected converter to be set")
	}
}

func TestWithCache(t *testing.T) {
	cache := NewMockCache()

	proxy := &Proxy{}
	opt := WithCache(cache)
	opt(proxy)

	if proxy.cache != cache {
		t.Error("Expected cache to be set")
	}
}

func TestWithDescriptorFetcher(t *testing.T) {
	fetcher := func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
		return []ocispecv1.Descriptor{}, nil
	}

	proxy := &Proxy{}
	opt := WithDescriptorFetcher(fetcher)
	opt(proxy)

	if proxy.fd == nil {
		t.Error("Expected descriptor fetcher to be set")
	}
}

func TestWithRemotePlainHttp(t *testing.T) {
	proxy := &Proxy{}
	opt := WithRemotePlainHttp()
	opt(proxy)

	if !proxy.plainHttp {
		t.Error("Expected plainHttp to be true")
	}
}

func TestWithRemoteInsecure(t *testing.T) {
	proxy := &Proxy{}
	opt := WithRemoteInsecure()
	opt(proxy)

	if !proxy.insecure {
		t.Error("Expected insecure to be true")
	}
}

func TestProxyServeHTTPWithPlatform(t *testing.T) {
	mockResolver := &MockResolver{
		fetchFunc: func(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("platform specific content"))), nil
		},
	}

	mockCache := NewMockCache()
	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      26,
		Digest:    "sha256:platformtest",
		Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "testimage",
		},
		Platform: &ocispecv1.Platform{
			OS:           "linux",
			Architecture: "amd64",
		},
	}

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			return []ocispecv1.Descriptor{descriptor}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/path/reference?title=testimage&platform=linux/amd64", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestProxyServeHTTPCacheHit(t *testing.T) {
	// Build the reference exactly as defaultConverter does for the test URL so
	// that the cache key written here matches the key the Proxy will look up.
	reqURL := "/path/reference?title=cachedimage"
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	ref, opts, err := defaultConverter(req)
	if err != nil {
		t.Fatalf("defaultConverter failed: %v", err)
	}

	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      14,
		Digest:    "sha256:cached",
		Annotations: map[string]string{
			ocispecv1.AnnotationTitle: opts[ocispecv1.AnnotationTitle],
		},
	}

	mockCache := NewMockCache()
	// Use the real cache key the Proxy will use on lookup.
	mockCache.Set(ref.String(), opts["platform"], opts[ocispecv1.AnnotationTitle], descriptor)

	mockResolver := &MockResolver{
		fetchFunc: func(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("cached content"))), nil
		},
	}

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		// fd should not be called on a true cache hit.
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			return []ocispecv1.Descriptor{descriptor}, nil
		},
	}

	w := httptest.NewRecorder()
	// Reuse the same request instance (already consumed above only for converter).
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, reqURL, nil))

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	if cacheHeader := w.Header().Get("X-Cache"); cacheHeader == "MISS" {
		t.Error("Expected cache HIT, got MISS — cache key mismatch")
	}
}

func TestProxyServeHTTPCacheMiss(t *testing.T) {
	mockResolver := &MockResolver{
		fetchFunc: func(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("new content"))), nil
		},
	}

	mockCache := NewMockCache()
	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      11,
		Digest:    "sha256:new",
		Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "newimage",
		},
	}

	proxy := &Proxy{
		resolver: mockResolver,
		cache:    mockCache,
		conv:     defaultConverter,
		fd: func(ctx context.Context, resolver registry.Resolver, ref reference.Reference, opts map[string]string) ([]ocispecv1.Descriptor, error) {
			return []ocispecv1.Descriptor{descriptor}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/path/reference?title=newimage", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check for X-Cache header
	if cacheHeader := w.Header().Get("X-Cache"); cacheHeader != "MISS" {
		t.Errorf("Expected X-Cache: MISS, got %s", cacheHeader)
	}
}
