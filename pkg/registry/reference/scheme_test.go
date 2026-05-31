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
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// GetScheme
// ---------------------------------------------------------------------------

func TestGetScheme_ValidSchemes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect Schema
	}{
		{"oci scheme", "oci://path/to/dir", OciScheme},
		{"registry scheme", "cr://docker.io/nginx", RegistryScheme},
		{"http scheme", "http://example.com", HttpSchema},
		{"https scheme", "https://example.com", HttpsSchema},
		{"s3 scheme", "s3://bucket/key", S3Schema},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, err := GetScheme(tt.input)
			if err != nil {
				t.Errorf("GetScheme(%q) unexpected error: %v", tt.input, err)
			}
			if scheme != tt.expect {
				t.Errorf("GetScheme(%q) = %q, want %q", tt.input, scheme, tt.expect)
			}
		})
	}
}

func TestGetScheme_InvalidScheme(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedError error
	}{
		{"docker unsupported scheme", "docker://something/path", ErrSchemeUnsupported},
		{"ftp unsupported scheme", "ftp://example.com", ErrSchemeUnsupported},
		{"no scheme", "docker.io/nginx", ErrSchemeRequired},
		{"empty string", "", ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetScheme(tt.input)
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("GetScheme(%q) error = %v, want %v", tt.input, err, tt.expectedError)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsRegistryScheme
// ---------------------------------------------------------------------------

func TestIsRegistryScheme_RegistrySchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme Schema
		expect bool
	}{
		{"OCI scheme", OciScheme, true},
		{"Container Registry scheme", RegistryScheme, true},
		{"File scheme", FileSchema, false},
		{"Dir scheme", DirSchema, false},
		{"S3 scheme", S3Schema, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRegistryScheme(tt.scheme)
			if result != tt.expect {
				t.Errorf("IsRegistryScheme(%v) = %v, want %v", tt.scheme, result, tt.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Schema.String - Additional tests
// ---------------------------------------------------------------------------

func TestSchema_String_AllSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme Schema
		expect string
	}{
		{"OCI", OciScheme, "oci://"},
		{"CR", RegistryScheme, "cr://"},
		{"Dir", DirSchema, "dir://"},
		{"File", FileSchema, "file://"},
		{"S3", S3Schema, "s3://"},
		{"S3+HTTP", S3httpSchema, "s3+http://"},
		{"HTTP", HttpSchema, "http://"},
		{"HTTPS", HttpsSchema, "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.scheme.String()
			if result != tt.expect {
				t.Errorf("String() = %q, want %q", result, tt.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Schema.IsPrefix - Additional tests
// ---------------------------------------------------------------------------

func TestSchema_IsPrefix_AllSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme Schema
		input  string
		expect bool
	}{
		{"oci positive", OciScheme, "oci://something", true},
		{"oci negative", OciScheme, "cr://something", false},
		{"cr positive", RegistryScheme, "cr://docker.io/nginx", true},
		{"cr negative", RegistryScheme, "oci://path", false},
		{"s3 positive", S3Schema, "s3://bucket/key", true},
		{"s3+http positive", S3httpSchema, "s3+http://bucket/key", true},
		{"http positive", HttpSchema, "http://example.com", true},
		{"https positive", HttpsSchema, "https://example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.scheme.IsPrefix(tt.input)
			if result != tt.expect {
				t.Errorf("IsPrefix(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

func TestSchema_IsPrefix_EdgeCases(t *testing.T) {
	t.Run("partial prefix match should fail", func(t *testing.T) {
		if HttpSchema.IsPrefix("htt://example.com") {
			t.Error("Should not match partial prefix")
		}
	})

	t.Run("case sensitive", func(t *testing.T) {
		if HttpSchema.IsPrefix("HTTP://example.com") {
			t.Error("IsPrefix should be case sensitive")
		}
	})

	t.Run("exact match", func(t *testing.T) {
		if !HttpSchema.IsPrefix("http://") {
			t.Error("Should match exact scheme string")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		if FileSchema.IsPrefix("") {
			t.Error("Should not match empty string")
		}
	})
}

// ---------------------------------------------------------------------------
// Schema.Eq
// ---------------------------------------------------------------------------

func TestSchema_Eq_AllSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme Schema
		value  string
		expect bool
	}{
		{"oci positive", OciScheme, "oci", true},
		{"oci negative", OciScheme, "Oci", false},
		{"cr positive", RegistryScheme, "cr", true},
		{"cr negative", RegistryScheme, "CR", false},
		{"file positive", FileSchema, "file", true},
		{"s3 positive", S3Schema, "s3", true},
		{"http positive", HttpSchema, "http", true},
		{"https positive", HttpsSchema, "https", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.scheme.Eq(tt.value)
			if result != tt.expect {
				t.Errorf("Eq(%q) = %v, want %v", tt.value, result, tt.expect)
			}
		})
	}
}

func TestSchema_Eq_CaseSensitive(t *testing.T) {
	if OciScheme.Eq("OCI") {
		t.Error("Eq should be case sensitive")
	}
	if OciScheme.Eq("Oci") {
		t.Error("Eq should be case sensitive")
	}
}

// ---------------------------------------------------------------------------
// Type checking functions - comprehensive coverage
// ---------------------------------------------------------------------------

func TestIsFile_Various(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"basic file", "file://path", true},
		{"file with absolute path", "file:///absolute/path", true},
		{"file with relative path", "file://relative/path", true},
		{"partial match", "fil://path", false},
		{"case sensitive", "File://path", false},
		{"no prefix", "path", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFile(tt.input)
			if result != tt.expect {
				t.Errorf("IsFile(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

func TestIsDir_Various(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"basic dir", "dir://path", true},
		{"dir with path", "dir://some/path", true},
		{"partial match", "di://path", false},
		{"case sensitive", "Dir://path", false},
		{"no prefix", "path", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsDir(tt.input)
			if result != tt.expect {
				t.Errorf("IsDir(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

func TestIsHTTP_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"http", "http://example.com", true},
		{"https", "https://example.com", true},
		{"http with path", "http://example.com/path", true},
		{"https with port", "https://example.com:8443/path", true},
		{"partial match http", "htt://example.com", false},
		{"partial match https", "http://example.com", true}, // has http prefix
		{"ftp", "ftp://example.com", false},
		{"case sensitive", "HTTP://example.com", false},
		{"file", "file://path", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsHTTP(tt.input)
			if result != tt.expect {
				t.Errorf("IsHTTP(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

func TestIsS3_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"s3", "s3://bucket/key", true},
		{"s3+http", "s3+http://bucket/key", true},
		{"s3 with path", "s3://bucket/path/to/key", true},
		{"s3+http with port", "s3+http://localhost:9000/bucket", true},
		{"partial s3", "s3+://bucket", false},
		{"https not s3", "https://bucket.s3.amazonaws.com", false},
		{"case sensitive", "S3://bucket", false},
		{"file", "file://bucket", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsS3(tt.input)
			if result != tt.expect {
				t.Errorf("IsS3(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

func TestIsOCI_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"oci", "oci://path", true},
		{"cr", "cr://host/image", true},
		{"oci with path", "oci:///absolute/path", true},
		{"cr with tag", "cr://docker.io/nginx:latest", true},
		{"file not oci", "file://path", false},
		{"http not oci", "http://example.com", false},
		{"s3 not oci", "s3://bucket", false},
		{"case sensitive", "OCI://path", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsOCI(tt.input)
			if result != tt.expect {
				t.Errorf("IsOCI(%q) = %v, want %v", tt.input, result, tt.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestSchemaConsistency(t *testing.T) {
	allSchemes := []Schema{
		OciScheme, RegistryScheme, DirSchema, FileSchema,
		S3Schema, S3httpSchema, HttpSchema, HttpsSchema,
	}

	for _, scheme := range allSchemes {
		schemeStr := scheme.String()

		// String should have ://
		if !strings.Contains(schemeStr, "://") {
			t.Errorf("Schema.String() %q should contain ://", schemeStr)
		}

		// IsPrefix should work with String output
		if !scheme.IsPrefix(schemeStr + "something") {
			t.Errorf("IsPrefix should match with Schema.String() output")
		}

		// Eq should work with String (without ://)
		if !scheme.Eq(string(scheme)) {
			t.Errorf("Eq should work with string(scheme)")
		}
	}
}
