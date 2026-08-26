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

package packer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackValidation tests pack file validation
func TestPackValidation(t *testing.T) {
	tests := []struct {
		name    string
		pack    Pack
		wantErr bool
		errMsg  string
	}{
		{
			name: "empty items",
			pack: Pack{
				Items: []Descriptor{},
			},
			wantErr: true,
			errMsg:  "pack file must contain at least one item",
		},
		{
			name: "missing from field",
			pack: Pack{
				Items: []Descriptor{
					{
						Type: "application/json",
					},
				},
			},
			wantErr: true,
			errMsg:  "'from' field is required",
		},
		{
			name: "invalid source format",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "invalid://source",
						Type: "application/json",
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid source format",
		},
		{
			name: "valid file source",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "file://config.json",
						Type: "application/json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid directory source",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "dir://data",
						Type: "application/json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid OCI source",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "oci://example.com/image:latest",
						Type: "application/vnd.oci.image.manifest.v1+json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid OCI Layout source",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "oci://./oci-layout",
						Type: "application/vnd.oci.image.manifest.v1+json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid HTTP source",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "https://example.com/file.tar.gz",
						Type: "application/gzip",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid config source in metadata",
			pack: Pack{
				Metadata: Metadata{
					Config: &ConfigDescriptor{
						From: "invalid://source",
					},
				},
				Items: []Descriptor{
					{
						From: "file://data.json",
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid source format",
		},
		{
			name: "missing from in metadata config",
			pack: Pack{
				Metadata: Metadata{
					Config: &ConfigDescriptor{},
				},
				Items: []Descriptor{
					{
						From: "file://data.json",
					},
				},
			},
			wantErr: true,
			errMsg:  "'from' field is required",
		},
		{
			name: "valid multiple items",
			pack: Pack{
				Items: []Descriptor{
					{
						From: "file://config.json",
						Type: "application/json",
					},
					{
						From: "dir://data",
					},
					{
						From: "https://example.com/file.tar.gz",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.pack)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error() == "" || !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error message = %v, want to contain %v", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

// TestIsValidSource tests the isValidSource function
func TestIsValidSource(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		valid bool
	}{
		{
			name:  "valid file schema",
			from:  "file://config.json",
			valid: true,
		},
		{
			name:  "valid dir schema",
			from:  "dir://data",
			valid: true,
		},
		{
			name:  "valid oci schema",
			from:  "oci://registry/image:tag",
			valid: true,
		},
		{
			name:  "valid layout schema",
			from:  "oci://./oci-layout",
			valid: true,
		},
		{
			name:  "valid http schema",
			from:  "http://example.com/file",
			valid: true,
		},
		{
			name:  "valid https schema",
			from:  "https://example.com/file",
			valid: true,
		},
		{
			name:  "valid s3 schema",
			from:  "s3://bucket/key",
			valid: true,
		},
		{
			name:  "valid docker schema",
			from:  "cr://image:tag",
			valid: true,
		},
		{
			name:  "invalid schema",
			from:  "invalid://source",
			valid: false,
		},
		{
			name:  "no schema",
			from:  "source",
			valid: false,
		},
		{
			name:  "empty string",
			from:  "",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidSource(tt.from)
			if got != tt.valid {
				t.Errorf("isValidSource(%q) = %v, want %v", tt.from, got, tt.valid)
			}
		})
	}
}

// TestFileMediaTypeResolution tests media type resolution for files
func TestFileMediaTypeResolution(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{
			filename: "archive.tar",
			expected: "application/vnd.oci.image.layer.v1.tar",
		},
		{
			filename: "config.json",
			expected: "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveFileMediaType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveFileMediaType(%q) = %q, want %q", tt.filename, got, tt.expected)
			}
		})
	}

	// Test system-dependent cases: list all acceptable values explicitly
	// instead of using ambiguous || chains so failures are easy to diagnose.
	allowedGz := []string{
		"application/vnd.oci.image.layer.v1.tar+gzip",
		"application/gzip",
	}
	gzType := ResolveFileMediaType("archive.tar.gz")
	if !mediaTypeOneOf(gzType, allowedGz) {
		t.Errorf("ResolveFileMediaType(\"archive.tar.gz\") = %q, want one of %v", gzType, allowedGz)
	}

	allowedZst := []string{
		"application/vnd.oci.image.layer.v1.tar+zstd",
		"application/octet-stream",
	}
	zstType := ResolveFileMediaType("archive.tar.zst")
	if !mediaTypeOneOf(zstType, allowedZst) {
		t.Errorf("ResolveFileMediaType(\"archive.tar.zst\") = %q, want one of %v", zstType, allowedZst)
	}
}

// mediaTypeOneOf reports whether s equals any of the candidates.
func mediaTypeOneOf(s string, candidates []string) bool {
	for _, c := range candidates {
		if s == c {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Pack.Validate — additional edge cases
// ---------------------------------------------------------------------------

func TestPackValidate_NilItems(t *testing.T) {
	p := Pack{} // Items field is nil, not just empty
	err := Validate(p)
	if err == nil {
		t.Fatal("expected error for nil items, got nil")
	}
	if !strings.Contains(err.Error(), "at least one item") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPackValidate_ItemWithMissingFrom_ReportsIndex(t *testing.T) {
	p := Pack{
		Items: []Descriptor{
			{From: "file://first.txt"},
			{From: ""}, // index 1
		},
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "item[1]") {
		t.Errorf("error should mention item index, got: %v", err)
	}
}

func TestPackValidate_ItemConfigMissingFrom(t *testing.T) {
	p := Pack{
		Items: []Descriptor{
			{
				From:   "file://data.bin",
				Config: &ConfigDescriptor{From: ""},
			},
		},
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("expected error for item config with empty 'from'")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error should mention config, got: %v", err)
	}
}

func TestPackValidate_ItemConfigInvalidSource(t *testing.T) {
	p := Pack{
		Items: []Descriptor{
			{
				From:   "file://data.bin",
				Config: &ConfigDescriptor{From: "ftp://nope"},
			},
		},
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("expected error for invalid item config source")
	}
}

func TestPackValidate_ItemConfigValidSource(t *testing.T) {
	// Positive case: item.Config with a valid source must not fail Validate.
	validSources := []string{
		"file://cfg.json",
		"https://example.com/cfg.json",
		"s3://bucket/cfg.json",
	}
	for _, src := range validSources {
		t.Run(src, func(t *testing.T) {
			p := Pack{
				Items: []Descriptor{
					{
						From:   "file://data.bin",
						Config: &ConfigDescriptor{From: src},
					},
				},
			}
			if err := Validate(p); err != nil {
				t.Errorf("Validate() unexpected error for item.Config.From=%q: %v", src, err)
			}
		})
	}
}

func TestPackValidate_MetadataConfigValidSources(t *testing.T) {
	validSources := []string{
		"file://config.json",
		"dir://configdir",
		"oci://registry/image:tag",
		"https://example.com/cfg",
		"s3://bucket/key",
	}
	for _, src := range validSources {
		t.Run(src, func(t *testing.T) {
			p := Pack{
				Metadata: Metadata{Config: &ConfigDescriptor{From: src}},
				Items:    []Descriptor{{From: "file://data.bin"}},
			}
			if err := Validate(p); err != nil {
				t.Errorf("Validate() unexpected error for valid config source %q: %v", src, err)
			}
		})
	}
}

func TestPackValidate_AllValidSchemasForItems(t *testing.T) {
	sources := []string{
		"file://a.bin",
		"dir://mydir",
		"oci://host/img:tag",
		"cr://host/img:tag",
		"http://example.com/f",
		"https://example.com/f",
		"s3://bucket/obj",
		"s3+http://bucket/obj",
	}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			p := Pack{Items: []Descriptor{{From: src}}}
			if err := Validate(p); err != nil {
				t.Errorf("Validate() unexpected error for %q: %v", src, err)
			}
		})
	}
}

func TestPackValidate_MultipleItemsFirstInvalid(t *testing.T) {
	p := Pack{
		Items: []Descriptor{
			{From: ""},
			{From: "file://ok.bin"},
		},
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "item[0]") {
		t.Errorf("error should reference item[0], got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// LoadFromFile
// ---------------------------------------------------------------------------

func TestLoadFromFile_ValidYAML(t *testing.T) {
	content := `
items:
  - from: file://data.bin
    type: application/octet-stream
`
	path := writeTempFile(t, "pack*.yaml", content)
	pack, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if len(pack.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(pack.Items))
	}
	if pack.Items[0].From != "file://data.bin" {
		t.Errorf("unexpected From: %q", pack.Items[0].From)
	}
	if pack.Items[0].Type != "application/octet-stream" {
		t.Errorf("unexpected Type: %q", pack.Items[0].Type)
	}
}

func TestLoadFromFile_WithAnnotationsAndConfig(t *testing.T) {
	content := `
type: application/custom
config:
  from: file://cfg.json
annotations:
  org.example.version: "1.0"
items:
  - from: https://example.com/blob.tar.gz
    annotations:
      org.example.name: myblob
`
	path := writeTempFile(t, "pack*.yaml", content)
	pack, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if pack.Type != "application/custom" {
		t.Errorf("Type = %q, want application/custom", pack.Type)
	}
	if pack.Config == nil || pack.Config.From != "file://cfg.json" {
		t.Errorf("Config.From unexpected: %+v", pack.Config)
	}
	if pack.Annotations["org.example.version"] != "1.0" {
		t.Errorf("annotation missing: %v", pack.Annotations)
	}
	if pack.Items[0].Annotations["org.example.name"] != "myblob" {
		t.Errorf("item annotation missing: %v", pack.Items[0].Annotations)
	}
}

func TestLoadFromFile_UnknownFieldRejected(t *testing.T) {
	content := `
items:
  - from: file://a.bin
unknown_field: oops
`
	path := writeTempFile(t, "pack*.yaml", content)
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoadFromFile_InvalidYAML(t *testing.T) {
	content := `items: [unclosed`
	path := writeTempFile(t, "pack*.yaml", content)
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected YAML parse error, got nil")
	}
}

func TestLoadFromFile_FileNotFound(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/pack.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadFromFile_EmptyFile(t *testing.T) {
	path := writeTempFile(t, "pack*.yaml", "")
	_, err := LoadFromFile(path)
	// yaml decoder returns EOF — that's an error
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestLoadFromFile_MultipleItems(t *testing.T) {
	content := `
items:
  - from: file://a.bin
  - from: dir://mydir
  - from: https://host.com/c.tgz
`
	path := writeTempFile(t, "pack*.yaml", content)
	pack, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if len(pack.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(pack.Items))
	}
}

// ---------------------------------------------------------------------------
// Descriptor.FileToOciDescriptor
// ---------------------------------------------------------------------------

func TestFileToOciDescriptor_NotAFile(t *testing.T) {
	d := Descriptor{From: "dir://something"}
	_, _, err := FileToOciDescriptor(d)
	if err == nil {
		t.Fatal("expected error for non-file schema")
	}
	if !strings.Contains(err.Error(), "not a file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFileToOciDescriptor_FileMissing(t *testing.T) {
	d := Descriptor{From: "file:///nonexistent/path/file.bin"}
	_, _, err := FileToOciDescriptor(d)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFileToOciDescriptor_Success(t *testing.T) {
	content := []byte("hello oci world")
	path := writeTempFile(t, "layer*.tar", string(content))

	d := Descriptor{
		From:        "file://" + path,
		Type:        "application/custom",
		Annotations: map[string]string{"key": "val"},
	}

	desc, rc, err := FileToOciDescriptor(d)
	if err != nil {
		t.Fatalf("FileToOciDescriptor() error: %v", err)
	}
	defer rc.Close()

	if desc.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", desc.Size, len(content))
	}
	if desc.ArtifactType != "application/custom" {
		t.Errorf("ArtifactType = %q, want application/custom", desc.ArtifactType)
	}
	if desc.Annotations["key"] != "val" {
		t.Errorf("annotation not set: %v", desc.Annotations)
	}
	if desc.Digest.String() == "" {
		t.Error("Digest should not be empty")
	}
}

func TestFileToOciDescriptor_MediaTypeInferredFromExtension(t *testing.T) {
	tests := []struct {
		ext           string
		wantMediaType string
	}{
		{".tar", "application/vnd.oci.image.layer.v1.tar"},
		{".tgz", "application/vnd.oci.image.layer.v1.tar+gzip"},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			path := writeTempFile(t, "layer*"+tt.ext, "data")
			d := Descriptor{From: "file://" + path}
			desc, rc, err := FileToOciDescriptor(d)
			if err != nil {
				t.Fatalf("FileToOciDescriptor() error: %v", err)
			}
			rc.Close()
			if desc.MediaType != tt.wantMediaType {
				t.Errorf("MediaType = %q, want %q", desc.MediaType, tt.wantMediaType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConfigDescriptor.ToDescriptor
// ---------------------------------------------------------------------------

func TestConfigDescriptor_ToDescriptor(t *testing.T) {
	cd := ConfigDescriptor{
		From:        "file://cfg.json",
		Type:        "application/json",
		Platform:    "linux/amd64",
		Annotations: map[string]string{"a": "b"},
	}
	d := cd.ToDescriptor()

	if d.From != cd.From {
		t.Errorf("From = %q, want %q", d.From, cd.From)
	}
	if d.Type != cd.Type {
		t.Errorf("Type = %q, want %q", d.Type, cd.Type)
	}
	if d.Platform != cd.Platform {
		t.Errorf("Platform = %q, want %q", d.Platform, cd.Platform)
	}
	if d.Annotations["a"] != "b" {
		t.Errorf("Annotations not copied: %v", d.Annotations)
	}
}

func TestConfigDescriptor_ToDescriptor_NilAnnotations(t *testing.T) {
	cd := ConfigDescriptor{From: "file://x.bin"}
	d := cd.ToDescriptor()
	if d.Annotations != nil {
		t.Errorf("expected nil annotations, got %v", d.Annotations)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeTempFile(t *testing.T, pattern, content string) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, filepath.Base(pattern))
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err = f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()
	return f.Name()
}
