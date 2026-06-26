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
	"errors"
	"reflect"
	"testing"
)

func TestParse_Successful(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedRef Reference
	}{
		{
			name:  "basic host and path",
			input: "cr://docker.io/library/nginx",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "library/nginx",
				Ref:    "latest",
			},
		},
		{
			name:  "host, path and tag",
			input: "cr://docker.io/library/nginx:1.19",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "library/nginx",
				Ref:    "1.19",
			},
		},
		{
			name:  "host, path and digest",
			input: "cr://docker.io/library/nginx@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "library/nginx",
				Ref:    "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			},
		},
		{
			name:  "simple host and single image name",
			input: "cr://localhost/myapp",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "localhost",
				Path:   "myapp",
				Ref:    "latest",
			},
		},
		{
			name:  "host with port and tag",
			input: "cr://localhost:5000/myapp:v1.0",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "localhost:5000",
				Path:   "myapp",
				Ref:    "v1.0",
			},
		},
		{
			name:  "deeply nested path with tag",
			input: "cr://registry.example.com/team/project/service:v2",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "registry.example.com",
				Path:   "team/project/service",
				Ref:    "v2",
			},
		},
		{
			name:  "digest with algorithm and colon",
			input: "cr://docker.io/nginx@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "nginx",
				Ref:    "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			},
		},
		{
			name:  "image name with dots and hyphens",
			input: "cr://registry.io/my-app.v2:1.0-alpha",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "registry.io",
				Path:   "my-app.v2",
				Ref:    "1.0-alpha",
			},
		},
		{
			name:  "numeric tag",
			input: "cr://docker.io/nginx:123",
			expectedRef: Reference{
				Scheme: RegistryScheme,
				Host:   "docker.io",
				Path:   "nginx",
				Ref:    "123",
			},
		},
		{
			name:  "OCI dir reference with numeric tag",
			input: "oci://relative/path/to/dir:nginx:123",
			expectedRef: Reference{
				Scheme: OciScheme,
				Path:   "relative/path/to/dir",
				Ref:    "nginx:123",
			},
		},
		{
			name:  "OCI dir reference with numeric tag, absolute path",
			input: "oci:///full/path/to/dir:docker.io/library/nginx:123",
			expectedRef: Reference{
				Scheme: OciScheme,
				Path:   "/full/path/to/dir",
				Ref:    "docker.io/library/nginx:123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			if !reflect.DeepEqual(ref, tt.expectedRef) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.input, ref, tt.expectedRef)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedError error
	}{
		{
			name:          "double slash in path (dummy:// is not in input)",
			input:         "cr://docker.io//library/nginx",
			expectedError: ErrInvalid,
		},
		{
			name:          "image with trailing slash before tag",
			input:         "cr://docker.io/myapp/:latest",
			expectedError: ErrInvalid,
		},
		{
			name:          "scheme like http://",
			input:         "http://docker.io/nginx",
			expectedError: ErrSchemeUnsupported,
		},
		{
			name:          "scheme like docker://",
			input:         "docker://registry/nginx",
			expectedError: ErrSchemeUnsupported,
		},
		{
			name:          "only // not ://",
			input:         "//host/path",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "empty string",
			input:         "",
			expectedError: ErrInvalid,
		},
		{
			name:          "only tag (leading colon)",
			input:         ":latest",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "only digest (leading @)",
			input:         "@sha256:abc123",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "path without hostname",
			input:         "/library/nginx:latest",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "host only, no path",
			input:         "docker.io",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "host with port only, no path",
			input:         "localhost:5000",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "multiple @ symbols",
			input:         "docker.io/myapp@tag@sha256:abc",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "multiple colons in image path",
			input:         "docker.io/image:with:colons",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "multiple colons with tag",
			input:         "docker.io/image:with:colons:v1",
			expectedError: ErrSchemeRequired,
		},
		{
			name:          "spaces in input",
			input:         "host port/myapp",
			expectedError: ErrSchemeRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := Parse(tt.input)
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Parse(%q) error = %v, want %v", tt.input, err, tt.expectedError)
			}
			zeroRef := Reference{}
			if !reflect.DeepEqual(ref, zeroRef) {
				t.Errorf("Parse(%q) returned non-zero reference on error: %+v", tt.input, ref)
			}
		})
	}
}

func TestParse_ReferenceSeparation(t *testing.T) {
	// @ takes precedence over : in separating Ref from Image
	t.Run("digest with colon inside", func(t *testing.T) {
		ref, err := Parse("cr://docker.io/myapp@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b")
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if ref.Path != "myapp" {
			t.Errorf("Image = %q, want %q", ref.Path, "myapp")
		}
		if ref.Ref != "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b" {
			t.Errorf("Ref = %q, want %q", ref.Ref, "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b")
		}
	})

	t.Run("colon separates tag when no @", func(t *testing.T) {
		ref, err := Parse("cr://docker.io/myapp:v1.0")
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if ref.Path != "myapp" {
			t.Errorf("Image = %q, want %q", ref.Path, "myapp")
		}
		if ref.Ref != "v1.0" {
			t.Errorf("Ref = %q, want %q", ref.Ref, "v1.0")
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add("docker.io/library/nginx:latest")
	f.Add("localhost/myapp")
	f.Add("registry:5000/app:v1")
	f.Add("docker.io/myapp@sha256:abc123")
	f.Add("")
	f.Add("://invalid")
	// Seeds that exercise valid cr:// and oci:// paths.
	f.Add("cr://docker.io/library/nginx:latest")
	f.Add("cr://localhost:5000/myapp:v1")
	f.Add("oci://relative/path:tag")
	f.Add("oci:///absolute/path:tag")

	f.Fuzz(func(t *testing.T, input string) {
		ref, err := Parse(input)

		if err == nil {
			// Valid output must have non-empty Host for cr:// references.
			if ref.Scheme == RegistryScheme && ref.Host == "" {
				t.Errorf("host should not be empty for valid cr:// input %q", input)
			}
			// Ref must not be empty.
			if ref.Ref == "" {
				t.Errorf("ref should not be empty for valid input %q", input)
			}
		} else {
			// Error must be one of the documented sentinel values.
			if !errors.Is(err, ErrInvalid) &&
				!errors.Is(err, ErrHostnameRequired) &&
				!errors.Is(err, ErrSchemeRequired) &&
				!errors.Is(err, ErrSchemeUnsupported) {
				t.Errorf("Parse(%q) returned undocumented error type %T: %v", input, err, err)
			}
		}
	})
}
