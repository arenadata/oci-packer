package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// MountFrom of a manifest mounts its config and layers AND pushes the manifest's
// own bytes into the destination — otherwise the destination's index would point
// at a manifest the repository does not hold.
func TestMountFrom_PushesManifestBytes(t *testing.T) {
	manifestData := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageManifest,
		Config:    ocispecv1.Descriptor{MediaType: ocispecv1.MediaTypeImageConfig, Digest: digest.FromString("config"), Size: 6},
		Layers:    []ocispecv1.Descriptor{{MediaType: ocispecv1.MediaTypeImageLayer, Digest: digest.FromString("layer1"), Size: 6}},
	}
	manifestBytes, _ := json.Marshal(manifestData)
	manifestDigest := digest.FromBytes(manifestBytes)

	var mu sync.Mutex
	var mounted, uploaded []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/manifests/") && r.Method == http.MethodHead:
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/manifests/") && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", ocispecv1.MediaTypeImageManifest)
			_, _ = w.Write(manifestBytes)
		case strings.Contains(r.URL.Path, "/blobs/uploads/") && r.Method == http.MethodPost && r.URL.Query().Get("mount") != "":
			mounted = append(mounted, r.URL.Query().Get("mount"))
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(r.URL.Path, "/blobs/") && r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound) // nothing exists yet in the destination
		case strings.Contains(r.URL.Path, "/blobs/uploads/") && r.Method == http.MethodPost:
			w.Header().Set("Location", "/v2/target/repo/blobs/uploads/session")
			w.WriteHeader(http.StatusAccepted)
		case strings.Contains(r.URL.Path, "/blobs/uploads/") && r.Method == http.MethodPut:
			uploaded = append(uploaded, r.URL.Query().Get("digest"))
			w.Header().Set("Docker-Content-Digest", r.URL.Query().Get("digest"))
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ref, _ := reference.Parse("cr://" + server.URL[7:] + "/target/repo:latest")
	client, _ := NewRegistryClient(ref, WithPlainHttp())
	c := client.(*Client)

	src, _ := reference.Parse("cr://" + server.URL[7:] + "/source/image:1.0")
	desc, err := c.MountFrom(context.Background(), src)
	require.NoError(t, err)
	require.Equal(t, manifestDigest, desc.Digest)

	mu.Lock()
	defer mu.Unlock()
	require.ElementsMatch(t, []string{digest.FromString("config").String(), digest.FromString("layer1").String()}, mounted,
		"config and layers are mounted cross-repo")
	require.Equal(t, []string{manifestDigest.String()}, uploaded, "the manifest's own bytes are pushed")
}
