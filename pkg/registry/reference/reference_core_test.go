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
	"testing"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// Reference.IsZero
// ---------------------------------------------------------------------------

func TestReference_IsZero_EmptyReference(t *testing.T) {
	ref := Reference{}
	if !ref.IsZero() {
		t.Error("Empty Reference should be zero")
	}
}

func TestReference_IsZero_NonZeroReference(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
	}{
		{
			name: "with scheme",
			ref: Reference{
				Scheme: RegistryScheme,
			},
		},
		{
			name: "with host",
			ref: Reference{
				Host: "docker.io",
			},
		},
		{
			name: "with path",
			ref: Reference{
				Path: "library/nginx",
			},
		},
		{
			name: "with ref",
			ref: Reference{
				Ref: "latest",
			},
		},
		{
			name: "full reference",
			ref: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "library/nginx",
				Ref:    "latest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ref.IsZero() {
				t.Errorf("Reference %+v should not be zero", tt.ref)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Reference.Descriptor & WithDescriptor
// ---------------------------------------------------------------------------

func TestReference_Descriptor_Empty(t *testing.T) {
	ref := Reference{}
	desc := ref.Descriptor()
	if desc.Size != 0 || desc.MediaType != "" {
		t.Errorf("Expected empty descriptor, got %+v", desc)
	}
}

func TestReference_WithDescriptor(t *testing.T) {
	original := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	descriptor := ocispecv1.Descriptor{
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Size:      1234,
		Digest:    "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
	}

	modified := original.WithDescriptor(descriptor)

	// Original should not be modified
	if original.Descriptor().Size != 0 {
		t.Error("Original reference should not be modified")
	}

	// Modified should have the descriptor
	if modified.Descriptor().Size != descriptor.Size {
		t.Errorf("Descriptor size = %d, want %d", modified.Descriptor().Size, descriptor.Size)
	}
	if modified.Descriptor().Digest != descriptor.Digest {
		t.Errorf("Descriptor digest = %s, want %s", modified.Descriptor().Digest, descriptor.Digest)
	}
	if modified.Descriptor().MediaType != descriptor.MediaType {
		t.Errorf("Descriptor media type = %s, want %s", modified.Descriptor().MediaType, descriptor.MediaType)
	}

	// Other fields should remain unchanged
	if modified.Scheme != original.Scheme || modified.Host != original.Host ||
		modified.Path != original.Path || modified.Ref != original.Ref {
		t.Error("WithDescriptor should not modify other fields")
	}
}

func TestReference_WithDescriptor_ChainedCalls(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	desc1 := ocispecv1.Descriptor{Size: 100}
	desc2 := ocispecv1.Descriptor{Size: 200}

	modified := ref.WithDescriptor(desc1).WithDescriptor(desc2)

	if modified.Descriptor().Size != 200 {
		t.Errorf("Expected descriptor size 200, got %d", modified.Descriptor().Size)
	}
}

// ---------------------------------------------------------------------------
// Reference.RepoReference
// ---------------------------------------------------------------------------

func TestReference_RepoReference_WithTag(t *testing.T) {
	ref := Reference{
		Path: "library/nginx",
		Ref:  "1.19",
	}

	repoRef := ref.RepoReference()
	expected := "library/nginx:1.19"
	if repoRef != expected {
		t.Errorf("RepoReference() = %q, want %q", repoRef, expected)
	}
}

func TestReference_RepoReference_WithDigest(t *testing.T) {
	digestStr := "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b"
	ref := Reference{
		Path: "library/nginx",
		Ref:  digestStr,
	}

	repoRef := ref.RepoReference()
	expected := "library/nginx@" + digestStr
	if repoRef != expected {
		t.Errorf("RepoReference() = %q, want %q", repoRef, expected)
	}
}

func TestReference_RepoReference_SinglePath(t *testing.T) {
	ref := Reference{
		Path: "nginx",
		Ref:  "latest",
	}

	repoRef := ref.RepoReference()
	expected := "nginx:latest"
	if repoRef != expected {
		t.Errorf("RepoReference() = %q, want %q", repoRef, expected)
	}
}

func TestReference_RepoReference_MultiLevelPath(t *testing.T) {
	ref := Reference{
		Path: "team/project/service/app",
		Ref:  "v1.2.3",
	}

	repoRef := ref.RepoReference()
	expected := "team/project/service/app:v1.2.3"
	if repoRef != expected {
		t.Errorf("RepoReference() = %q, want %q", repoRef, expected)
	}
}

// ---------------------------------------------------------------------------
// Reference.Merge
// ---------------------------------------------------------------------------

func TestReference_Merge_EmptySecond(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	empty := Reference{}
	merged := base.Merge(empty)

	// Verify all fields are preserved
	if merged.Scheme != base.Scheme || merged.Host != base.Host ||
		merged.Path != base.Path || merged.Ref != base.Ref {
		t.Errorf("Merge with empty reference should return base unchanged")
	}
}

func TestReference_Merge_ReplaceTag(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	update := Reference{
		Ref: "1.19.5",
	}

	merged := base.Merge(update)

	if merged.Scheme != base.Scheme {
		t.Errorf("Scheme should not change")
	}
	if merged.Host != base.Host {
		t.Errorf("Host should not change")
	}
	if merged.Path != base.Path {
		t.Errorf("Path should not change")
	}
	if merged.Ref != "1.19.5" {
		t.Errorf("Ref = %q, want 1.19.5", merged.Ref)
	}
}

func TestReference_Merge_ReplacePathAndRef(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	update := Reference{
		Path: "library/redis",
		Ref:  "6.0",
	}

	merged := base.Merge(update)

	if merged.Host != base.Host {
		t.Errorf("Host should not change")
	}
	if merged.Path != "library/redis" {
		t.Errorf("Path = %q, want library/redis", merged.Path)
	}
	if merged.Ref != "6.0" {
		t.Errorf("Ref = %q, want 6.0", merged.Ref)
	}
}

func TestReference_Merge_PreservesNonEmptyFields(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}

	// Update with non-empty Scheme (should not affect since Scheme is not merged)
	update := Reference{
		Scheme: OciScheme,
		Path:   "alpine",
		Ref:    "3.14",
	}

	merged := base.Merge(update)

	// Scheme doesn't get merged, so it stays as RegistryScheme
	if merged.Scheme != base.Scheme {
		t.Errorf("Scheme should not change during merge")
	}
	if merged.Path != "alpine" {
		t.Errorf("Path should be updated")
	}
	if merged.Ref != "3.14" {
		t.Errorf("Ref should be updated")
	}
	// Verify other fields are preserved
	if merged.Host != base.Host {
		t.Errorf("Host should remain unchanged")
	}
}

func TestReference_Merge_EmptyPathKeepsBase(t *testing.T) {
	base := Reference{
		Path: "original/path",
		Ref:  "original",
	}

	update := Reference{
		Ref: "updated", // Only update Ref
	}

	merged := base.Merge(update)

	if merged.Path != "original/path" {
		t.Errorf("Path = %q, want original/path (unchanged)", merged.Path)
	}
	if merged.Ref != "updated" {
		t.Errorf("Ref = %q, want updated", merged.Ref)
	}
}
