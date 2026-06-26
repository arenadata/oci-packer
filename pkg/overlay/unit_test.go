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

package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readUnit(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read unit %s: %v", name, err)
	}
	return string(b)
}

func TestWriteUnits_OverlayOnly(t *testing.T) {
	dir := t.TempDir()
	names, err := WriteUnits(UnitOptions{
		Overlay:   MountOptions{LowerDirs: []string{"/blobs/l1", "/blobs/l2"}, Target: "/mnt/rootfs"},
		UnitDir:   dir,
		SourceRef: "oci://./layout:tag",
	})
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	if len(names) != 1 || names[0] != "mnt-rootfs.mount" {
		t.Fatalf("unexpected unit names: %v", names)
	}

	body := readUnit(t, dir, "mnt-rootfs.mount")
	if !strings.Contains(body, "Type=overlay") {
		t.Errorf("missing Type=overlay:\n%s", body)
	}
	// bottom-to-top {l1,l2} must be written top-to-bottom: l2:l1
	if !strings.Contains(body, "Options=lowerdir=/blobs/l2:/blobs/l1,ro") {
		t.Errorf("wrong lowerdir options:\n%s", body)
	}
	if !strings.Contains(body, "Where=/mnt/rootfs") {
		t.Errorf("missing Where:\n%s", body)
	}
}

func TestWriteUnits_AutoTmpfs(t *testing.T) {
	dir := t.TempDir()
	names, err := WriteUnits(UnitOptions{
		Overlay: MountOptions{LowerDirs: []string{"/blobs/l1"}, Target: "/mnt/rootfs"},
		Tmpfses: []TmpfsOptions{{Target: "/mnt/rootfs/tmp"}},
		UnitDir: dir,
	})
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected overlay + tmpfs units, got %v", names)
	}

	body := readUnit(t, dir, "mnt-rootfs-tmp.mount")
	for _, want := range []string{"Type=tmpfs", "After=mnt-rootfs.mount", "BindsTo=mnt-rootfs.mount"} {
		if !strings.Contains(body, want) {
			t.Errorf("tmpfs unit missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Options=size=") {
		t.Errorf("tmpfs without size must not set Options=size=:\n%s", body)
	}
}

func TestWriteUnits_TmpfsSize(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteUnits(UnitOptions{
		Overlay: MountOptions{LowerDirs: []string{"/blobs/l1"}, Target: "/mnt/rootfs"},
		Tmpfses: []TmpfsOptions{
			{Target: "/mnt/rootfs/tmp", Size: "512m"},
			{Target: "/mnt/rootfs/run", Size: "64m"},
			{Target: "/mnt/rootfs/var/tmp"},
		},
		UnitDir: dir,
	})
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}

	if body := readUnit(t, dir, "mnt-rootfs-tmp.mount"); !strings.Contains(body, "Options=size=512m") {
		t.Errorf("/tmp missing size=512m:\n%s", body)
	}
	if body := readUnit(t, dir, "mnt-rootfs-run.mount"); !strings.Contains(body, "Options=size=64m") {
		t.Errorf("/run missing size=64m:\n%s", body)
	}
	if body := readUnit(t, dir, "mnt-rootfs-var-tmp.mount"); strings.Contains(body, "Options=size=") {
		t.Errorf("/var/tmp without size must not set Options=size=:\n%s", body)
	}
}

func TestWriteUnits_WithBinds(t *testing.T) {
	dir := t.TempDir()
	names, err := WriteUnits(UnitOptions{
		Overlay: MountOptions{LowerDirs: []string{"/blobs/l1"}, Target: "/mnt/rootfs"},
		Binds:   []BindOptions{{Source: "/data", Target: "/mnt/rootfs/var/lib/app"}},
		UnitDir: dir,
	})
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected overlay + bind units, got %v", names)
	}

	body := readUnit(t, dir, "mnt-rootfs-var-lib-app.mount")
	for _, want := range []string{
		"What=/data", "Where=/mnt/rootfs/var/lib/app", "Type=none", "Options=bind",
		"After=mnt-rootfs.mount", "BindsTo=mnt-rootfs.mount",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("bind unit missing %q:\n%s", want, body)
		}
	}
}

func TestWriteUnits_LowerdirTooLong(t *testing.T) {
	dir := t.TempDir()
	var lowers []string
	for i := 0; i < 200; i++ {
		lowers = append(lowers, "/very/long/blob/path/sha256/"+strings.Repeat("a", 32))
	}
	_, err := WriteUnits(UnitOptions{
		Overlay: MountOptions{LowerDirs: lowers, Target: "/mnt/rootfs"},
		UnitDir: dir,
	})
	if err == nil {
		t.Fatal("expected error for oversized lowerdir")
	}
	// nothing must be written
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("no units should be written on error, found %d", len(entries))
	}
}

func TestEscapePath(t *testing.T) {
	cases := map[string]string{
		"/mnt/rootfs":     "mnt-rootfs",
		"/mnt/rootfs/tmp": "mnt-rootfs-tmp",
		"/var/lib/app":    "var-lib-app",
		"/mnt/my-dir":     `mnt-my\x2ddir`,
		"/":               "-",
	}
	for in, want := range cases {
		if got := escapePath(in); got != want {
			t.Errorf("escapePath(%q) = %q, want %q", in, got, want)
		}
	}
}
