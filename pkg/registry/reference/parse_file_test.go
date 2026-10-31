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
	"strings"
	"testing"
)

const hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseFile(t *testing.T) {
	tests := []struct {
		in   string
		ref  Reference
		file string
	}{
		{"oci://remote.registry.com/packs/application:v1.2.3:path/to/file",
			Reference{Scheme: OciScheme, Host: "remote.registry.com", Path: "packs/application", Ref: "v1.2.3"}, "path/to/file"},
		{"oci://localhost:5000/packs/app:v1:schema.json",
			Reference{Scheme: OciScheme, Host: "localhost:5000", Path: "packs/app", Ref: "v1"}, "schema.json"},
		{"oci://reg.io/app@sha256:" + hex + ":templates/a.ptmpl",
			Reference{Scheme: OciScheme, Host: "reg.io", Path: "app", Ref: "sha256:" + hex}, "templates/a.ptmpl"},
		// a path may hold colons; a leading slash and dots are cleaned
		{"oci://reg.io/app:v1:/./etc/a:b.conf",
			Reference{Scheme: OciScheme, Host: "reg.io", Path: "app", Ref: "v1"}, "etc/a:b.conf"},
	}
	for _, tc := range tests {
		ref, file, err := ParseFile(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(ref, tc.ref) || file != tc.file {
			t.Errorf("%s:\n got %+v %q\nwant %+v %q", tc.in, ref, file, tc.ref, tc.file)
		}
	}
}

func TestParseFile_Refused(t *testing.T) {
	tests := map[string]error{
		"oci://reg.io/app:v1":            ErrFileRequired, // the artifact, no file
		"oci://reg.io/app":               ErrFileRequired,
		"oci://reg.io/app:v1:":           ErrFileRequired,
		"oci://reg.io/app@sha256:" + hex: ErrFileRequired,
		"oci://reg.io/app@sha256":        ErrInvalid,
		"docker://reg.io/app:v1:a":       ErrSchemeUnsupported,
		"reg.io/app:v1:a":                ErrSchemeRequired,
		"oci://reg.io":                   ErrInvalid,
	}
	for in, want := range tests {
		if _, _, err := ParseFile(in); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", in, err, want)
		}
	}
	for _, in := range []string{"oci://reg.io/app:v1:../secret", "oci://reg.io/app:v1:a/../../b", "oci://reg.io/app:v1:.", "oci://./layout:repo:v1:file"} {
		if _, _, err := ParseFile(in); err == nil {
			t.Errorf("%s: must be refused", in)
		}
	}
	_, _, err := ParseFile("oci://./layout:repo:v1:file")
	if err == nil || !strings.Contains(err.Error(), "registry reference") {
		t.Errorf("a layout: %v", err)
	}
}
