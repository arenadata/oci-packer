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
	"sync"
	"testing"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewMemoryCache(t *testing.T) {
	cache := newMemoryCache()

	if cache == nil {
		t.Fatal("Expected cache to be created, got nil")
	}
}

func TestMemoryCacheSet(t *testing.T) {
	cache := newMemoryCache()

	desc := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      1234,
		Digest:    "sha256:abcd1234",
	}

	cache.Set("repo1", "linux/amd64", "latest", desc)

	// Retrieve the value
	retrieved := cache.Get("repo1", "linux/amd64", "latest")
	if retrieved.Digest != desc.Digest {
		t.Errorf("Expected digest %s, got %s", desc.Digest, retrieved.Digest)
	}
	if retrieved.Size != desc.Size {
		t.Errorf("Expected size %d, got %d", desc.Size, retrieved.Size)
	}
}

func TestMemoryCacheGet(t *testing.T) {
	cache := newMemoryCache()

	desc := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      5678,
		Digest:    "sha256:efgh5678",
	}

	cache.Set("myrepo", "darwin/arm64", "v1.0", desc)

	retrieved := cache.Get("myrepo", "darwin/arm64", "v1.0")
	if retrieved.Digest != desc.Digest {
		t.Errorf("Expected digest %s, got %s", desc.Digest, retrieved.Digest)
	}
}

func TestMemoryCacheGetNonExistent(t *testing.T) {
	cache := newMemoryCache()

	// Try to get a non-existent key
	retrieved := cache.Get("nonexistent", "linux/amd64", "latest")
	if retrieved.Digest != "" {
		t.Errorf("Expected empty descriptor, got %v", retrieved)
	}
}

func TestMemoryCacheDel(t *testing.T) {
	cache := newMemoryCache()

	desc := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.container.image.v1+json",
		Size:      9999,
		Digest:    "sha256:ijkl9999",
	}

	cache.Set("myrepo", "linux/amd64", "latest", desc)

	// Verify it exists
	retrieved := cache.Get("myrepo", "linux/amd64", "latest")
	if retrieved.Digest == "" {
		t.Fatal("Expected descriptor to exist before deletion")
	}

	// Delete it
	cache.Del("myrepo", "linux/amd64", "latest")

	// Verify it's deleted
	retrieved = cache.Get("myrepo", "linux/amd64", "latest")
	if retrieved.Digest != "" {
		t.Errorf("Expected empty descriptor after deletion, got %v", retrieved)
	}
}

func TestMemoryCacheMultipleRepos(t *testing.T) {
	cache := newMemoryCache()

	desc1 := ocispecv1.Descriptor{
		Digest: "sha256:1111",
		Size:   111,
	}

	desc2 := ocispecv1.Descriptor{
		Digest: "sha256:2222",
		Size:   222,
	}

	cache.Set("repo1", "linux/amd64", "latest", desc1)
	cache.Set("repo2", "linux/amd64", "latest", desc2)

	retrieved1 := cache.Get("repo1", "linux/amd64", "latest")
	retrieved2 := cache.Get("repo2", "linux/amd64", "latest")

	if retrieved1.Digest != desc1.Digest {
		t.Errorf("Expected digest %s for repo1, got %s", desc1.Digest, retrieved1.Digest)
	}

	if retrieved2.Digest != desc2.Digest {
		t.Errorf("Expected digest %s for repo2, got %s", desc2.Digest, retrieved2.Digest)
	}
}

func TestMemoryCacheMultiplePlatforms(t *testing.T) {
	cache := newMemoryCache()

	descAmd64 := ocispecv1.Descriptor{
		Digest: "sha256:amd64",
		Size:   1000,
	}

	descArm64 := ocispecv1.Descriptor{
		Digest: "sha256:arm64",
		Size:   2000,
	}

	cache.Set("myrepo", "linux/amd64", "latest", descAmd64)
	cache.Set("myrepo", "linux/arm64", "latest", descArm64)

	retrievedAmd64 := cache.Get("myrepo", "linux/amd64", "latest")
	retrievedArm64 := cache.Get("myrepo", "linux/arm64", "latest")

	if retrievedAmd64.Digest != descAmd64.Digest {
		t.Errorf("Expected AMD64 digest %s, got %s", descAmd64.Digest, retrievedAmd64.Digest)
	}

	if retrievedArm64.Digest != descArm64.Digest {
		t.Errorf("Expected ARM64 digest %s, got %s", descArm64.Digest, retrievedArm64.Digest)
	}
}

func TestMemoryCacheMultipleTitles(t *testing.T) {
	cache := newMemoryCache()

	desc1 := ocispecv1.Descriptor{
		Digest: "sha256:v1",
		Size:   100,
	}

	desc2 := ocispecv1.Descriptor{
		Digest: "sha256:v2",
		Size:   200,
	}

	cache.Set("myrepo", "linux/amd64", "v1.0", desc1)
	cache.Set("myrepo", "linux/amd64", "v2.0", desc2)

	retrieved1 := cache.Get("myrepo", "linux/amd64", "v1.0")
	retrieved2 := cache.Get("myrepo", "linux/amd64", "v2.0")

	if retrieved1.Digest != desc1.Digest {
		t.Errorf("Expected v1 digest %s, got %s", desc1.Digest, retrieved1.Digest)
	}

	if retrieved2.Digest != desc2.Digest {
		t.Errorf("Expected v2 digest %s, got %s", desc2.Digest, retrieved2.Digest)
	}
}

func TestMemoryCacheConcurrency(t *testing.T) {
	cache := newMemoryCache()
	wg := sync.WaitGroup{}

	// Run concurrent sets and gets
	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()
			desc := ocispecv1.Descriptor{
				Size:   int64(idx),
				Digest: "sha256:test",
			}
			cache.Set("repo", "linux/amd64", "version", desc)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_ = cache.Get("repo", "linux/amd64", "version")
		}()
	}

	wg.Wait()

	// Verify final state
	desc := cache.Get("repo", "linux/amd64", "version")
	if desc.Digest == "" {
		t.Fatal("Expected descriptor in cache after concurrent operations")
	}
}

func TestMemoryCacheOverwrite(t *testing.T) {
	cache := newMemoryCache()

	desc1 := ocispecv1.Descriptor{
		Digest: "sha256:first",
		Size:   100,
	}

	desc2 := ocispecv1.Descriptor{
		Digest: "sha256:second",
		Size:   200,
	}

	cache.Set("repo", "linux/amd64", "latest", desc1)
	retrieved := cache.Get("repo", "linux/amd64", "latest")
	if retrieved.Digest != desc1.Digest {
		t.Errorf("Expected first digest, got %s", retrieved.Digest)
	}

	// Overwrite with new value
	cache.Set("repo", "linux/amd64", "latest", desc2)
	retrieved = cache.Get("repo", "linux/amd64", "latest")
	if retrieved.Digest != desc2.Digest {
		t.Errorf("Expected second digest after overwrite, got %s", retrieved.Digest)
	}
}

func TestMemoryCacheEmptyKey(t *testing.T) {
	cache := newMemoryCache()

	desc := ocispecv1.Descriptor{
		Digest: "sha256:test",
		Size:   100,
	}

	cache.Set("", "", "test", desc)
	retrieved := cache.Get("", "", "test")

	if retrieved.Digest != desc.Digest {
		t.Errorf("Expected descriptor with empty keys, got %v", retrieved)
	}
}
