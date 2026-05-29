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
	"testing"
)

// ---------------------------------------------------------------------------
// Schema helpers
// ---------------------------------------------------------------------------

func TestSchema_String(t *testing.T) {
	if FileSchema.String() != "file://" {
		t.Errorf("FileSchema.String() = %q, want file://", FileSchema.String())
	}
	if DirSchema.String() != "dir://" {
		t.Errorf("DirSchema.String() = %q, want dir://", DirSchema.String())
	}
}

func TestSchema_IsPrefix(t *testing.T) {
	if !FileSchema.IsPrefix("file://something") {
		t.Error("IsPrefix should match file:// prefix")
	}
	if FileSchema.IsPrefix("dir://something") {
		t.Error("IsPrefix should not match wrong prefix")
	}
}

func TestSchema_Eq(t *testing.T) {
	if !OciScheme.Eq("oci") {
		t.Error("OciScheme.Eq(\"oci\") should be true")
	}
	if OciScheme.Eq("cr") {
		t.Error("OciScheme.Eq(\"cr\") should be false")
	}
}

// ---------------------------------------------------------------------------
// IsFile / IsDir / IsHTTP / IsS3 / IsOCI
// ---------------------------------------------------------------------------

func TestIsFile(t *testing.T) {
	if !IsFile("file://path") {
		t.Error("IsFile should return true for file:// prefix")
	}
	if IsFile("dir://path") {
		t.Error("IsFile should return false for dir:// prefix")
	}
}

func TestIsDir(t *testing.T) {
	if !IsDir("dir://path") {
		t.Error("IsDir should return true for dir:// prefix")
	}
	if IsDir("file://path") {
		t.Error("IsDir should return false for file:// prefix")
	}
}

func TestIsHTTP(t *testing.T) {
	if !IsHTTP("http://example.com/f") {
		t.Error("IsHTTP should be true for http://")
	}
	if !IsHTTP("https://example.com/f") {
		t.Error("IsHTTP should be true for https://")
	}
	if IsHTTP("ftp://example.com/f") {
		t.Error("IsHTTP should be false for ftp://")
	}
}

func TestIsS3(t *testing.T) {
	if !IsS3("s3://bucket/key") {
		t.Error("IsS3 should be true for s3://")
	}
	if !IsS3("s3+http://bucket/key") {
		t.Error("IsS3 should be true for s3+http://")
	}
	if IsS3("https://bucket.s3.amazonaws.com/key") {
		t.Error("IsS3 should be false for https://")
	}
}

func TestIsOCI(t *testing.T) {
	if !IsOCI("oci://path") {
		t.Error("IsOCI should be true for oci://")
	}
	if !IsOCI("cr://host/image") {
		t.Error("IsOCI should be true for cr://")
	}
	if IsOCI("file://path") {
		t.Error("IsOCI should be false for file://")
	}
}

// ---------------------------------------------------------------------------
// Reference.String
// ---------------------------------------------------------------------------

func TestReference_String_TagSeparator(t *testing.T) {
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}
	got := ref.String()
	if got != "cr://docker.io/library/nginx:latest" {
		t.Errorf("String() = %q (unexpected format for tag)", got)
	}
}

func TestReference_String_DigestSeparator(t *testing.T) {
	digestRef := "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b"
	ref := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    digestRef,
	}
	got := ref.String()
	if !containsStr(got, "@") {
		t.Errorf("String() = %q, expected @ separator for digest ref", got)
	}
	if !containsStr(got, digestRef) {
		t.Errorf("String() = %q, should contain digest %q", got, digestRef)
	}
}

func TestReference_String_OciSchemeNoHost(t *testing.T) {
	ref := Reference{
		Scheme: OciScheme,
		Path:   "relative/path",
		Ref:    "latest",
	}
	got := ref.String()
	// For OCI scheme the format is "oci" + path + ":" + ref (no host in string)
	if !containsStr(got, "relative/path") {
		t.Errorf("String() = %q, should contain path", got)
	}
}

// ---------------------------------------------------------------------------
// ParseRegistryReference
// ---------------------------------------------------------------------------

func TestParseRegistryReference_SimpleTag(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}
	parsedRef, err := ParseRegistryReference("v1.2.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := base.Merge(parsedRef)
	if result.Ref != "v1.2.3" {
		t.Errorf("Ref = %q, want v1.2.3", parsedRef.Ref)
	}
	if result.Host != base.Host {
		t.Errorf("Host changed unexpectedly: %q", parsedRef.Host)
	}
}

func TestParseRegistryReference_EmptyRefKeepsBase(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "stable",
	}
	parsedRef, err := ParseRegistryReference("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := base.Merge(parsedRef)
	if result.Ref != "stable" {
		t.Errorf("Ref = %q, want stable (unchanged)", result.Ref)
	}
}

func TestParseRegistryReference_FullCRRef(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "docker.io",
		Path:   "library/nginx",
		Ref:    "latest",
	}
	parsedRef, err := ParseRegistryReference("cr://docker.io/other/image:v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := base.Merge(parsedRef)
	if result.Path != "other/image" {
		t.Errorf("Path = %q, want other/image", result.Path)
	}
	if result.Ref != "v2" {
		t.Errorf("Ref = %q, want v2", result.Ref)
	}
}

func TestParseRegistryReference_UnsupportedSchemeInRef(t *testing.T) {
	_, err := ParseRegistryReference("https://something:tag")
	if !errors.Is(err, ErrSchemeUnsupported) {
		t.Errorf("expected ErrSchemeUnsupported, got %v", err)
	}
}

func TestParseRegistryReference_PathOnlyRef(t *testing.T) {
	base := Reference{
		Scheme: RegistryScheme,
		Host:   "myregistry.io",
		Path:   "old/path",
		Ref:    "latest",
	}
	// A ref like "myapp:v3" without "://" triggers parsePath
	parsedRef, err := ParseRegistryReference("myapp:v3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := base.Merge(parsedRef)
	if result.Path != "myapp" {
		t.Errorf("Path = %q, want myapp", result.Path)
	}
	if result.Ref != "v3" {
		t.Errorf("Ref = %q, want v3", result.Ref)
	}
}

func TestParseRegistryReference_DigestRef(t *testing.T) {
	base := Reference{Scheme: RegistryScheme, Host: "docker.io", Path: "lib/app", Ref: "latest"}
	dgst := "sha256:e58fcf7418d4390dec8e8fb69d88c06ec07039d651fedd3aa72af9972e7d046b"
	parsedRef, err := ParseRegistryReference(dgst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := base.Merge(parsedRef)
	// A long sha-like string without special chars — treated as tag replacement
	if result.Ref != dgst {
		t.Errorf("Ref = %q, want %q", result.Ref, dgst)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
