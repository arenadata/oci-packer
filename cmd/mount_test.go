//go:build linux

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

package cmd

import "testing"

func TestParseBinds(t *testing.T) {
	binds, err := parseBinds("/mnt/rootfs", []string{"/data:/var/lib/app", "/cfg:/etc/app"})
	if err != nil {
		t.Fatalf("parseBinds: %v", err)
	}
	if len(binds) != 2 {
		t.Fatalf("expected 2 binds, got %d", len(binds))
	}
	if binds[0].Source != "/data" || binds[0].Target != "/mnt/rootfs/var/lib/app" {
		t.Errorf("bind[0] = %+v", binds[0])
	}
	if binds[1].Source != "/cfg" || binds[1].Target != "/mnt/rootfs/etc/app" {
		t.Errorf("bind[1] = %+v", binds[1])
	}
}

func TestParseBinds_Invalid(t *testing.T) {
	for _, spec := range []string{"noseparator", ":/abs", "/host:"} {
		if _, err := parseBinds("/mnt", []string{spec}); err == nil {
			t.Errorf("parseBinds(%q) should fail", spec)
		}
	}
}

func TestParseTmpfsSizes(t *testing.T) {
	sizes, err := parseTmpfsSizes([]string{"/tmp:512m", "/run:64m"})
	if err != nil {
		t.Fatalf("parseTmpfsSizes: %v", err)
	}
	if sizes["/tmp"] != "512m" || sizes["/run"] != "64m" {
		t.Errorf("unexpected sizes: %v", sizes)
	}
}

func TestParseTmpfsSizes_Invalid(t *testing.T) {
	for _, spec := range []string{"nosize", "/tmp:", ":512m"} {
		if _, err := parseTmpfsSizes([]string{spec}); err == nil {
			t.Errorf("parseTmpfsSizes(%q) should fail", spec)
		}
	}
}
