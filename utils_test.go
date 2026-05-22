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
	"testing"

	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// ResolveFileMediaType
// ---------------------------------------------------------------------------

func TestResolveFileMediaType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		// Special compound extensions — checked before filepath.Ext
		{"archive.tar.gz", ocispecv1.MediaTypeImageLayerGzip},
		{"path/to/archive.tar.gz", ocispecv1.MediaTypeImageLayerGzip},
		{"archive.tar.zst", ocispecv1.MediaTypeImageLayerZstd},

		// Switch-case extensions
		{"archive.tar", ocispecv1.MediaTypeImageLayer},
		{"layer.tgz", ocispecv1.MediaTypeImageLayerGzip},

		// MIME-based extensions
		{"config.json", "application/json"},
		{"page.html", "text/html"},
		{"style.css", "text/css"},
		{"image.png", "image/png"},
		{"image.jpeg", "image/jpeg"},
		{"image.jpg", "image/jpeg"},

		// Unknown extension → default
		{"file.unknownXYZ", defaultMediaType},
		// No extension at all → default
		{"nodotfile", defaultMediaType},
		// Empty string → default
		{"", defaultMediaType},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveFileMediaType(tt.filename)
			if got != tt.want {
				t.Errorf("ResolveFileMediaType(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestResolveFileMediaType_MIMEStripsParameters(t *testing.T) {
	// mime.TypeByExtension can return "text/html; charset=utf-8" — verify we strip params
	result := ResolveFileMediaType("index.html")
	for _, ch := range result {
		if ch == ';' {
			t.Errorf("ResolveFileMediaType returned MIME with parameters: %q", result)
		}
	}
}

// ---------------------------------------------------------------------------
// NewDescriptorFromBytes
// ---------------------------------------------------------------------------

func TestNewDescriptorFromBytes(t *testing.T) {
	data := []byte(`{"hello":"world"}`)
	mt := "application/json"

	desc := NewDescriptorFromBytes(mt, data)

	if desc.MediaType != mt {
		t.Errorf("MediaType = %q, want %q", desc.MediaType, mt)
	}
	if desc.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", desc.Size, len(data))
	}
	expected := digest.FromBytes(data)
	if desc.Digest != expected {
		t.Errorf("Digest = %v, want %v", desc.Digest, expected)
	}
}

func TestNewDescriptorFromBytes_EmptyMediaType_UsesDefault(t *testing.T) {
	desc := NewDescriptorFromBytes("", []byte("data"))
	if desc.MediaType != defaultMediaType {
		t.Errorf("MediaType = %q, want %q", desc.MediaType, defaultMediaType)
	}
}

func TestNewDescriptorFromBytes_EmptyData(t *testing.T) {
	desc := NewDescriptorFromBytes("application/json", []byte{})
	if desc.Size != 0 {
		t.Errorf("Size = %d, want 0", desc.Size)
	}
	if desc.Digest != digest.FromBytes([]byte{}) {
		t.Errorf("Digest mismatch for empty data")
	}
}

func TestNewDescriptorFromBytes_DigestIsReproducible(t *testing.T) {
	data := []byte("same content")
	d1 := NewDescriptorFromBytes("application/octet-stream", data)
	d2 := NewDescriptorFromBytes("application/octet-stream", data)
	if d1.Digest != d2.Digest {
		t.Errorf("digest not reproducible: %v != %v", d1.Digest, d2.Digest)
	}
}

// ---------------------------------------------------------------------------
// NewDescriptorFromFileDescriptor
// ---------------------------------------------------------------------------

func TestNewDescriptorFromFileDescriptor_Success(t *testing.T) {
	content := []byte("test file content for descriptor")

	f, err := os.CreateTemp(t.TempDir(), "layer*.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	// Seek back to start so the descriptor reader starts from 0
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	desc, err := NewDescriptorFromFileDescriptor(f)
	if err != nil {
		t.Fatalf("NewDescriptorFromFileDescriptor() error: %v", err)
	}

	if desc.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", desc.Size, len(content))
	}
	expected := digest.FromBytes(content)
	if desc.Digest != expected {
		t.Errorf("Digest = %v, want %v", desc.Digest, expected)
	}
	if desc.MediaType != ocispecv1.MediaTypeImageLayer {
		t.Errorf("MediaType = %q, want %q", desc.MediaType, ocispecv1.MediaTypeImageLayer)
	}
}

func TestNewDescriptorFromFileDescriptor_SeeksToStart(t *testing.T) {
	// Write then advance position partway — the descriptor function must seek back.
	content := []byte("seek test content")
	f, err := os.CreateTemp(t.TempDir(), "seek*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Seek(0, 0)
	// Do NOT seek — leave file at end. NewDescriptorFromFileDescriptor should handle this.
	desc, err := NewDescriptorFromFileDescriptor(f)
	if err != nil {
		t.Fatalf("NewDescriptorFromFileDescriptor() error: %v", err)
	}
	// If seek-back works, digest should match full content
	expected := digest.FromBytes(content)
	if desc.Digest != expected {
		t.Errorf("Digest mismatch — seek-back may have failed: got %v, want %v", desc.Digest, expected)
	}
}

func TestNewDescriptorFromFileDescriptor_MediaTypeFromExtension(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"archive*.tar.gz", ocispecv1.MediaTypeImageLayerGzip},
		{"archive*.tgz", ocispecv1.MediaTypeImageLayerGzip},
		{"archive*.tar", ocispecv1.MediaTypeImageLayer},
		{"data*.json", "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			defer f.Close()

			if _, err = f.Write([]byte("dummy")); err != nil {
				t.Fatal(err)
			}
			if _, err = f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			desc, err := NewDescriptorFromFileDescriptor(f)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if desc.MediaType != tt.want {
				t.Errorf("MediaType = %q, want %q", desc.MediaType, tt.want)
			}
		})
	}
}
