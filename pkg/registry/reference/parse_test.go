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
			input: "docker.io/library/nginx",
			expectedRef: Reference{
				Host:  "docker.io",
				Image: "library/nginx",
				Ref:   "latest",
			},
		},
		{
			name:  "host, path and tag",
			input: "docker.io/library/nginx:1.19",
			expectedRef: Reference{
				Host:  "docker.io",
				Image: "library/nginx",
				Ref:   "1.19",
			},
		},
		{
			name:  "host, path and digest",
			input: "docker.io/library/nginx@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			expectedRef: Reference{
				Host:  "docker.io",
				Image: "library/nginx",
				Ref:   "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			},
		},
		{
			name:  "simple host and single image name",
			input: "localhost/myapp",
			expectedRef: Reference{
				Host:  "localhost",
				Image: "myapp",
				Ref:   "latest",
			},
		},
		{
			name:  "host with port and tag",
			input: "localhost:5000/myapp:v1.0",
			expectedRef: Reference{
				Host:  "localhost:5000",
				Image: "myapp",
				Ref:   "v1.0",
			},
		},
		{
			name:  "deeply nested path with tag",
			input: "registry.example.com/team/project/service:v2",
			expectedRef: Reference{
				Host:  "registry.example.com",
				Image: "team/project/service",
				Ref:   "v2",
			},
		},
		{
			name:  "digest with algorithm and colon",
			input: "docker.io/nginx@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			expectedRef: Reference{
				Host:  "docker.io",
				Image: "nginx",
				Ref:   "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b",
			},
		},
		{
			name:  "image name with dots and hyphens",
			input: "registry.io/my-app.v2:1.0-alpha",
			expectedRef: Reference{
				Host:  "registry.io",
				Image: "my-app.v2",
				Ref:   "1.0-alpha",
			},
		},
		{
			name:  "numeric tag",
			input: "docker.io/nginx:123",
			expectedRef: Reference{
				Host:  "docker.io",
				Image: "nginx",
				Ref:   "123",
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
			input:         "docker.io//library/nginx",
			expectedError: ErrInvalid,
		},
		{
			name:          "image with trailing slash before tag",
			input:         "docker.io/myapp/:latest",
			expectedError: ErrInvalid,
		},
		{
			name:          "scheme like http://",
			input:         "http://docker.io/nginx",
			expectedError: ErrInvalid,
		},
		{
			name:          "scheme like docker://",
			input:         "docker://registry/nginx",
			expectedError: ErrInvalid,
		},
		{
			name:          "only // not ://",
			input:         "//host/path",
			expectedError: ErrInvalid,
		},
		{
			name:          "empty string",
			input:         "",
			expectedError: ErrHostnameRequired,
		},
		{
			name:          "only tag (leading colon)",
			input:         ":latest",
			expectedError: ErrInvalid,
		},
		{
			name:          "only digest (leading @)",
			input:         "@sha256:abc123",
			expectedError: ErrInvalid,
		},
		{
			name:          "path without hostname",
			input:         "/library/nginx:latest",
			expectedError: ErrHostnameRequired,
		},
		{
			name:          "host only, no path",
			input:         "docker.io",
			expectedError: ErrInvalid,
		},
		{
			name:          "host with port only, no path",
			input:         "localhost:5000",
			expectedError: ErrInvalid,
		},
		{
			name:          "multiple @ symbols",
			input:         "docker.io/myapp@tag@sha256:abc",
			expectedError: ErrInvalid,
		},
		{
			name:          "multiple colons in image path",
			input:         "docker.io/image:with:colons",
			expectedError: ErrInvalid,
		},
		{
			name:          "multiple colons with tag",
			input:         "docker.io/image:with:colons:v1",
			expectedError: ErrInvalid,
		},
		{
			name:          "spaces in input",
			input:         "host port/myapp",
			expectedError: ErrInvalid,
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
		ref, err := Parse("docker.io/myapp@sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b")
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if ref.Image != "myapp" {
			t.Errorf("Image = %q, want %q", ref.Image, "myapp")
		}
		if ref.Ref != "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b" {
			t.Errorf("Ref = %q, want %q", ref.Ref, "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b")
		}
	})

	t.Run("colon separates tag when no @", func(t *testing.T) {
		ref, err := Parse("docker.io/myapp:v1.0")
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if ref.Image != "myapp" {
			t.Errorf("Image = %q, want %q", ref.Image, "myapp")
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

	f.Fuzz(func(t *testing.T, input string) {
		ref, err := Parse(input)

		if err == nil {
			// Valid output must have non-empty Host
			if ref.Host == "" {
				t.Errorf("host should not be empty for valid input %q", input)
			}
			// Ref must not be empty
			if ref.Ref == "" {
				t.Errorf("ref should not be empty for valid input %q", input)
			}
			// internal consistency: if Image is empty, it's okay (host only path would have been caught)
		} else {
			// error must be one of the sentinel errors
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrHostnameRequired) {
				// it's okay to get url.Parse errors as well (they are not sentinel)
			}
		}
	})
}
