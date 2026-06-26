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

package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// mockFetcher serves blobs keyed by their digest string.
type mockFetcher struct {
	blobs map[string][]byte
}

func (m mockFetcher) Fetch(_ context.Context, ref reference.Reference) (io.ReadCloser, error) {
	b, ok := m.blobs[ref.Ref]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m mockFetcher) FetchReference(context.Context, reference.Reference) (ocispecv1.Descriptor, io.ReadCloser, error) {
	return ocispecv1.Descriptor{}, nil, nil
}

func manifestDesc(plat string) ocispecv1.Descriptor {
	p := platforms.MustParse(plat)
	data := []byte("manifest-" + plat)
	return ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
		Platform:  &p,
	}
}

func indexFetcher(t *testing.T, children ...ocispecv1.Descriptor) (mockFetcher, ocispecv1.Descriptor) {
	t.Helper()
	index := ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: children,
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	return mockFetcher{blobs: map[string][]byte{desc.Digest.String(): data}}, desc
}

func TestSelectPlatform_PicksMatch(t *testing.T) {
	amd64 := manifestDesc("linux/amd64")
	arm64 := manifestDesc("linux/arm64")
	src, indexDesc := indexFetcher(t, amd64, arm64)

	got, err := SelectPlatform(context.Background(), src, indexDesc, platforms.Only(platforms.MustParse("linux/arm64")))
	if err != nil {
		t.Fatalf("SelectPlatform: %v", err)
	}
	if got.Digest != arm64.Digest {
		t.Errorf("selected %s, want arm64 %s", got.Digest, arm64.Digest)
	}
}

func TestSelectPlatform_NoMatch(t *testing.T) {
	src, indexDesc := indexFetcher(t, manifestDesc("linux/amd64"))

	_, err := SelectPlatform(context.Background(), src, indexDesc, platforms.Only(platforms.MustParse("windows/amd64")))
	if err == nil {
		t.Fatal("expected error when no manifest matches the platform")
	}
}

func TestSelectPlatform_SingleManifestPassthrough(t *testing.T) {
	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("single")),
	}
	got, err := SelectPlatform(context.Background(), mockFetcher{}, desc, platforms.Only(platforms.MustParse("linux/amd64")))
	if err != nil {
		t.Fatalf("SelectPlatform: %v", err)
	}
	if got.Digest != desc.Digest {
		t.Errorf("passthrough changed descriptor: %s != %s", got.Digest, desc.Digest)
	}
}
