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

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	packerhttp "github.com/arenadata/oci-packer/pkg/http"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ValidReference(t *testing.T) {
	ref, err := New("cr://registry.example.com/library/image:latest")
	require.NoError(t, err)
	assert.NotNil(t, ref)
}

func TestNew_InvalidReference(t *testing.T) {
	ref, err := New("oci:///invalid/path")
	assert.Error(t, err)
	assert.Nil(t, ref)
}

func TestNewRegistryClient_ValidReference(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, err := NewRegistryClient(parsedRef)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewRegistryClient_UnsupportedScheme(t *testing.T) {
	parsedRef := reference.Reference{
		Scheme: reference.OciScheme,
		Path:   "/some/path",
	}
	client, err := NewRegistryClient(parsedRef)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Equal(t, reference.ErrSchemeUnsupported, err)
}

func TestNewRegistryClient_WithPlainHttp(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, err := NewRegistryClient(parsedRef, WithPlainHttp())
	require.NoError(t, err)
	assert.NotNil(t, client)

	c := client.(*Client)
	assert.True(t, c.plainHttp)
}

func TestNewRegistryClient_WithInsecure(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, err := NewRegistryClient(parsedRef, WithInsecure())
	require.NoError(t, err)

	c := client.(*Client)
	assert.True(t, c.insecure)
}

func TestNewRegistryClient_WithCreds(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, err := NewRegistryClient(parsedRef, WithCreds("user", "pass"))
	require.NoError(t, err)

	c := client.(*Client)
	assert.Equal(t, "user", c.login)
	assert.Equal(t, "pass", c.password)
}

func TestNewRegistryClient_WithCustomClient(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	customClient := packerhttp.New()
	client, err := NewRegistryClient(parsedRef, WithClient(customClient))
	require.NoError(t, err)

	c := client.(*Client)
	assert.Equal(t, customClient, c.client)
}

func TestResolve_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dgst := digest.FromString("test-manifest")
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Extract hostname and port from http://localhost:port and create cr:// URL
	refStr := "cr://" + server.URL[7:] + "/library/image:latest"
	ref, _ := reference.Parse(refStr)
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	desc, err := c.Resolve(context.Background(), resolveRef)
	require.NoError(t, err)
	assert.Equal(t, ocispecv1.MediaTypeImageManifest, desc.MediaType)
	assert.Equal(t, int64(1024), desc.Size)
}

func TestResolve_MissingContentDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	_, err := c.Resolve(context.Background(), resolveRef)
	assert.Error(t, err)
	assert.Equal(t, registry.ErrEmptyDockerContentDigest, err)
}

func TestResolve_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	_, err := c.Resolve(context.Background(), resolveRef)
	assert.Error(t, err)
}

func TestExists_BlobExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dgst := digest.FromString("test-blob")
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	blobRef := reference.Reference{Ref: digest.FromString("test-blob").String()}
	exists, err := c.Exists(context.Background(), blobRef)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestExists_BlobNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	blobRef := reference.Reference{Ref: digest.FromString("test-blob").String()}
	exists, err := c.Exists(context.Background(), blobRef)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestFetchReference_Success(t *testing.T) {
	manifestData := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispecv1.Descriptor{
			MediaType: ocispecv1.MediaTypeImageConfig,
			Digest:    digest.FromString("config"),
			Size:      512,
		},
		Layers: []ocispecv1.Descriptor{
			{
				MediaType: ocispecv1.MediaTypeImageLayer,
				Digest:    digest.FromString("layer1"),
				Size:      1024,
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifestData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "manifests") {
			dgst := digest.FromBytes(manifestBytes)
			w.Header().Set("Docker-Content-Digest", dgst.String())
			w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifestBytes)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(manifestBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	fetchRef := reference.Reference{}
	desc, reader, err := c.FetchReference(context.Background(), fetchRef)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	assert.Equal(t, ocispecv1.MediaTypeImageManifest, desc.MediaType)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, manifestBytes, data)
}

func TestFetch_Success(t *testing.T) {
	blobData := []byte("test blob content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(blobData)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	fetchRef := reference.Reference{Ref: digest.FromBytes(blobData).String()}
	reader, err := c.Fetch(context.Background(), fetchRef)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, blobData, data)
}

// TestFetch_ManifestUsesManifestsEndpoint pins the endpoint split. A registry
// stores manifests apart from blobs and answers 404 for a manifest digest under
// /blobs/, which is what used to break every 'copy cr://... oci://...' at the
// very first fetch.
func TestFetch_ManifestUsesManifestsEndpoint(t *testing.T) {
	manifestBytes := []byte(`{"schemaVersion":2}`)

	for _, tc := range []struct {
		name      string
		mediaType string
		wantPath  string
	}{
		{"oci manifest", ocispecv1.MediaTypeImageManifest, "manifests"},
		{"oci index", ocispecv1.MediaTypeImageIndex, "manifests"},
		{"docker manifest", images.MediaTypeDockerSchema2Manifest, "manifests"},
		{"docker manifest list", images.MediaTypeDockerSchema2ManifestList, "manifests"},
		{"layer", ocispecv1.MediaTypeImageLayerGzip, "blobs"},
		{"config", ocispecv1.MediaTypeImageConfig, "blobs"},
		{"no descriptor attached", "", "blobs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requested, accept string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested, accept = r.URL.Path, r.Header.Get("Accept")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(manifestBytes)
			}))
			defer server.Close()

			ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
			client, _ := NewRegistryClient(ref, WithPlainHttp())

			dgst := digest.FromBytes(manifestBytes)
			desc := ocispecv1.Descriptor{MediaType: tc.mediaType, Digest: dgst, Size: int64(len(manifestBytes))}

			fetchRef := reference.Reference{Ref: dgst.String()}
			if tc.mediaType != "" {
				fetchRef = fetchRef.WithDescriptor(desc)
			}

			reader, err := client.Fetch(context.Background(), fetchRef)
			require.NoError(t, err)
			defer func() { _ = reader.Close() }()

			assert.Equal(t, "/v2/library/image/"+tc.wantPath+"/"+dgst.String(), requested)
			if tc.wantPath == "manifests" {
				assert.Contains(t, accept, tc.mediaType,
					"a manifest request must say which manifest type it accepts")
			}
		})
	}
}

func TestFetch_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	fetchRef := reference.Reference{Ref: digest.FromString("nonexistent").String()}
	_, err := c.Fetch(context.Background(), fetchRef)
	assert.Error(t, err)
}

func TestPush_Success(t *testing.T) {
	uploadCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// Check if blob exists
			w.WriteHeader(http.StatusNotFound)
		} else if r.Method == http.MethodPost {
			// Initiate upload
			uploadCount++
			w.Header().Set("Location", "/v2/library/image/blobs/uploads/uuid-123?_nouploadcache=true")
			w.WriteHeader(http.StatusAccepted)
		} else if r.Method == http.MethodPut {
			// Complete upload
			uploadCount++
			dgst := digest.FromString("test-blob")
			w.Header().Set("Docker-Content-Digest", dgst.String())
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageLayer,
		Digest:    digest.FromString("test-blob"),
		Size:      1024,
	}

	reader := bytes.NewReader([]byte("blob content"))
	err := c.Push(context.Background(), desc, reader)
	require.NoError(t, err)
	assert.Equal(t, 2, uploadCount)
}

func TestPush_BlobAlreadyExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// Blob exists
			dgst := digest.FromString("test-blob")
			w.Header().Set("Docker-Content-Digest", dgst.String())
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageLayer,
		Digest:    digest.FromString("test-blob"),
		Size:      1024,
	}

	reader := bytes.NewReader([]byte("blob content"))
	err := c.Push(context.Background(), desc, reader)
	assert.Error(t, err)
	assert.Equal(t, registry.ErrAlreadyExists, err)
}

func TestSetTag_ManifestMediaType(t *testing.T) {
	manifestData := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispecv1.Descriptor{
			MediaType: ocispecv1.MediaTypeImageConfig,
			Digest:    digest.FromString("config"),
			Size:      512,
		},
	}
	manifestBytes, _ := json.Marshal(manifestData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(manifestBytes)
		} else if r.Method == http.MethodPut {
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifestBytes).String())
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Digest:    digest.FromString("manifest"),
		Size:      int64(len(manifestBytes)),
	}

	err := c.SetTag(context.Background(), desc)
	require.NoError(t, err)
}

func TestSetTag_UnsupportedMediaType(t *testing.T) {
	ref, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, _ := NewRegistryClient(ref)
	c := client.(*Client)

	desc := ocispecv1.Descriptor{
		MediaType: "application/unsupported",
		Digest:    digest.FromString("blob"),
		Size:      1024,
	}

	err := c.SetTag(context.Background(), desc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported media type")
}

func TestGetManifestMediaType_Normal(t *testing.T) {
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("Content-Type", ocispecv1.MediaTypeImageManifest)

	mediaType := getManifestMediaType(resp)
	assert.Equal(t, ocispecv1.MediaTypeImageManifest, mediaType)
}

func TestGetManifestMediaType_WithEncoding(t *testing.T) {
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("Content-Type", ocispecv1.MediaTypeImageManifest+"; charset=utf-8")

	mediaType := getManifestMediaType(resp)
	assert.Equal(t, ocispecv1.MediaTypeImageManifest, mediaType)
}

func TestGetManifestMediaType_PlainTextSchema1(t *testing.T) {
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("Content-Type", "text/plain")

	mediaType := getManifestMediaType(resp)
	assert.Equal(t, images.MediaTypeDockerSchema1Manifest, mediaType)
}

func TestUrlFromReference_ValidReference(t *testing.T) {
	ref, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client, _ := NewRegistryClient(ref)
	c := client.(*Client)

	childRef := reference.Reference{Path: "library/subimage", Ref: "v1.0"}
	url, err := c.urlFromReference(childRef)
	require.NoError(t, err)
	assert.NotEmpty(t, url)
}

func TestMountFrom_Success(t *testing.T) {
	manifestData := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispecv1.Descriptor{
			MediaType: ocispecv1.MediaTypeImageConfig,
			Digest:    digest.FromString("config"),
			Size:      512,
		},
		Layers: []ocispecv1.Descriptor{
			{
				MediaType: ocispecv1.MediaTypeImageLayer,
				Digest:    digest.FromString("layer1"),
				Size:      1024,
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifestData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "manifests") && r.Method == http.MethodHead {
			dgst := digest.FromBytes(manifestBytes)
			w.Header().Set("Docker-Content-Digest", dgst.String())
			w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
			w.WriteHeader(http.StatusOK)
		} else if strings.Contains(r.URL.Path, "blobs") && r.Method == http.MethodPost && r.URL.Query().Get("mount") != "" {
			// Mount request
			w.WriteHeader(http.StatusCreated)
		} else if strings.Contains(r.URL.Path, "blobs/uploads") && r.Method == http.MethodPost {
			// The manifest's own bytes are pushed after its children are mounted.
			w.Header().Set("Location", "/v2/library/image/blobs/uploads/session")
			w.WriteHeader(http.StatusAccepted)
		} else if strings.Contains(r.URL.Path, "blobs/uploads") && r.Method == http.MethodPut {
			w.Header().Set("Docker-Content-Digest", r.URL.Query().Get("digest"))
			w.WriteHeader(http.StatusCreated)
		} else if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(manifestBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	mountRef := reference.Reference{}
	desc, err := c.MountFrom(context.Background(), mountRef)
	require.NoError(t, err)
	assert.Equal(t, ocispecv1.MediaTypeImageManifest, desc.MediaType)
}

func TestResolveRef_InvalidDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "invalid-digest")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	_, err := c.Resolve(context.Background(), resolveRef)
	assert.Error(t, err)
}

func TestDockerSchema2ManifestMediaType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dgst := digest.FromString("test-manifest")
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.Header().Set("Content-Type", images.MediaTypeDockerSchema2Manifest)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	desc, err := c.Resolve(context.Background(), resolveRef)
	require.NoError(t, err)
	assert.Equal(t, images.MediaTypeDockerSchema2Manifest, desc.MediaType)
}

func TestImageIndex_Resolve(t *testing.T) {
	indexData := ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Manifests: []ocispecv1.Descriptor{
			{
				MediaType: ocispecv1.MediaTypeImageManifest,
				Digest:    digest.FromString("manifest1"),
				Size:      1024,
				Platform: &ocispecv1.Platform{
					OS:           "linux",
					Architecture: "amd64",
				},
			},
		},
	}
	indexBytes, _ := json.Marshal(indexData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dgst := digest.FromBytes(indexBytes)
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.Header().Set("Content-Type", ocispecv1.MediaTypeImageIndex)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	desc, err := c.Resolve(context.Background(), resolveRef)
	require.NoError(t, err)
	assert.Equal(t, ocispecv1.MediaTypeImageIndex, desc.MediaType)
}

func TestMultipleOptions_Combined(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	customClient := packerhttp.New()

	client, err := NewRegistryClient(
		parsedRef,
		WithPlainHttp(),
		WithInsecure(),
		WithCreds("user", "pass"),
		WithClient(customClient),
	)
	require.NoError(t, err)

	c := client.(*Client)
	assert.True(t, c.plainHttp)
	assert.True(t, c.insecure)
	assert.Equal(t, "user", c.login)
	assert.Equal(t, "pass", c.password)
	assert.Equal(t, customClient, c.client)
}

func TestResolve_InvalidContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dgst := digest.FromString("test-manifest")
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
		// No Content-Length header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	resolveRef := reference.Reference{}
	desc, err := c.Resolve(context.Background(), resolveRef)
	require.NoError(t, err)
	assert.Equal(t, int64(0), desc.Size)
}

func TestFetchJson_Success(t *testing.T) {
	manifestData := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispecv1.Descriptor{
			MediaType: ocispecv1.MediaTypeImageConfig,
			Digest:    digest.FromString("config"),
			Size:      512,
		},
	}
	manifestBytes, _ := json.Marshal(manifestData)

	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifestBytes)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Digest:    digest.FromString("manifest"),
		Size:      int64(len(manifestBytes)),
	}

	var manifest ocispecv1.Manifest
	_, err := c.fetchJson(context.Background(), desc, "other/repo", &manifest)
	require.NoError(t, err)
	assert.Equal(t, manifestData.SchemaVersion, manifest.SchemaVersion)

	// The manifest is read by digest, from the repository being mounted out of.
	assert.Equal(t, "/v2/other/repo/manifests/"+desc.Digest.String(), requested)
}

func TestUrlFromReference_EmptyPath(t *testing.T) {
	ref, _ := reference.Parse("cr://registry.example.com/image:latest")
	client, _ := NewRegistryClient(ref)
	c := client.(*Client)

	childRef := reference.Reference{}
	url, err := c.urlFromReference(childRef)
	require.NoError(t, err)
	assert.NotEmpty(t, url)
}

func TestResolveRef_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/library/image:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolveRef := reference.Reference{}
	_, err := c.Resolve(ctx, resolveRef)
	assert.Error(t, err)
}
