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
	"net/http"
	"net/url"
	"testing"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDefaultConverter(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		queryParams      map[string]string
		expectedPath     string
		expectedRef      string
		expectedTitle    string
		expectedPlatform string
		expectedError    bool
	}{
		{
			name: "Valid request with title",
			path: "/registry/image:latest",
			queryParams: map[string]string{
				"title": "myimage",
			},
			expectedPath:     "/registry",
			expectedRef:      "image:latest",
			expectedTitle:    "myimage",
			expectedPlatform: "",
			expectedError:    false,
		},
		{
			name: "Valid request with title and platform",
			path: "/registry/image:latest",
			queryParams: map[string]string{
				"title":    "myimage",
				"platform": "linux/amd64",
			},
			expectedPath:     "/registry",
			expectedRef:      "image:latest",
			expectedTitle:    "myimage",
			expectedPlatform: "linux/amd64",
			expectedError:    false,
		},
		{
			name: "Missing title parameter",
			path: "/registry/image:latest",
			queryParams: map[string]string{
				"platform": "linux/amd64",
			},
			expectedError: true,
		},
		{
			name: "Invalid path without slash",
			path: "/invalidpath",
			queryParams: map[string]string{
				"title": "test",
			},
			expectedError: true,
		},
		{
			name: "Root path with single slash",
			path: "/image:latest",
			queryParams: map[string]string{
				"title": "test",
			},
			expectedError: true,
		},
		{
			name: "Nested path",
			path: "/registry/v1/namespace/image:v1.0.0",
			queryParams: map[string]string{
				"title": "myimage",
			},
			expectedPath:     "/registry/v1/namespace",
			expectedRef:      "image:v1.0.0",
			expectedTitle:    "myimage",
			expectedPlatform: "",
			expectedError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build query string
			queryStr := ""
			for k, v := range tt.queryParams {
				if queryStr != "" {
					queryStr += "&"
				}
				queryStr += k + "=" + v
			}

			req := &http.Request{
				URL: &url.URL{
					Path:     tt.path,
					RawQuery: queryStr,
				},
			}

			ref, opts, err := defaultConverter(req)

			if tt.expectedError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectedError {
				if ref.Path != tt.expectedPath {
					t.Errorf("Expected path %s, got %s", tt.expectedPath, ref.Path)
				}

				if ref.Ref != tt.expectedRef {
					t.Errorf("Expected ref %s, got %s", tt.expectedRef, ref.Ref)
				}

				if opts[ocispecv1.AnnotationTitle] != tt.expectedTitle {
					t.Errorf("Expected title %s, got %s", tt.expectedTitle, opts[ocispecv1.AnnotationTitle])
				}

				if platform, ok := opts["platform"]; ok && platform != "" {
					if platform != tt.expectedPlatform {
						t.Errorf("Expected platform %s, got %s", tt.expectedPlatform, platform)
					}
				}
			}
		})
	}
}

func TestDefaultConverterEmptyTitle(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{
			Path:     "/registry/image:latest",
			RawQuery: "title=",
		},
	}

	_, _, err := defaultConverter(req)
	if err == nil {
		t.Error("Expected error for empty title")
	}
}

func TestDefaultConverterComplexPath(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{
			Path:     "/docker/library/alpine:3.18",
			RawQuery: "title=alpine-image&platform=linux/arm64",
		},
	}

	ref, opts, err := defaultConverter(req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if ref.Path != "/docker/library" {
		t.Errorf("Expected path /docker/library, got %s", ref.Path)
	}

	if ref.Ref != "alpine:3.18" {
		t.Errorf("Expected ref alpine:3.18, got %s", ref.Ref)
	}

	if opts[ocispecv1.AnnotationTitle] != "alpine-image" {
		t.Errorf("Expected title alpine-image, got %s", opts[ocispecv1.AnnotationTitle])
	}

	if opts["platform"] != "linux/arm64" {
		t.Errorf("Expected platform linux/arm64, got %s", opts["platform"])
	}
}

func TestDefaultConverterMultipleSlashes(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{
			Path:     "/a/b/c/d/e/image:tag",
			RawQuery: "title=test",
		},
	}

	ref, _, err := defaultConverter(req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should split at the last slash
	if ref.Path != "/a/b/c/d/e" {
		t.Errorf("Expected path /a/b/c/d/e, got %s", ref.Path)
	}

	if ref.Ref != "image:tag" {
		t.Errorf("Expected ref image:tag, got %s", ref.Ref)
	}
}

func TestDefaultConverterWithAdditionalQueryParams(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{
			Path:     "/registry/image:latest",
			RawQuery: "title=test&platform=linux/amd64&extra=param&another=value",
		},
	}

	_, opts, err := defaultConverter(req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if opts[ocispecv1.AnnotationTitle] != "test" {
		t.Errorf("Expected title test, got %s", opts[ocispecv1.AnnotationTitle])
	}

	if opts["platform"] != "linux/amd64" {
		t.Errorf("Expected platform linux/amd64, got %s", opts["platform"])
	}

	// Extra params should not be in opts
	if _, ok := opts["extra"]; ok {
		t.Error("Unexpected extra param in opts")
	}
	if _, ok := opts["another"]; ok {
		t.Error("Unexpected another param in opts")
	}
}

func TestDefaultConverterImageWithoutTag(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{
			Path:     "/registry/image",
			RawQuery: "title=test",
		},
	}

	ref, opts, err := defaultConverter(req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if ref.Path != "/registry" {
		t.Errorf("Expected path /registry, got %s", ref.Path)
	}

	if ref.Ref != "image" {
		t.Errorf("Expected ref image, got %s", ref.Ref)
	}

	if opts[ocispecv1.AnnotationTitle] != "test" {
		t.Errorf("Expected title test, got %s", opts[ocispecv1.AnnotationTitle])
	}
}
