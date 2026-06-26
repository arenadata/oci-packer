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

func TestOverlayOptions_ReversesOrder(t *testing.T) {
	// bottom-to-top {l1,l2,l3} => top-to-bottom l3:l2:l1
	opts, err := overlayOptions([]string{"/l1", "/l2", "/l3"})
	if err != nil {
		t.Fatalf("overlayOptions: %v", err)
	}
	if opts != "lowerdir=/l3:/l2:/l1,ro" {
		t.Errorf("got %q", opts)
	}
}

func TestOverlayOptions_Empty(t *testing.T) {
	if _, err := overlayOptions(nil); err == nil {
		t.Fatal("expected error for empty lower dirs")
	}
}

func TestOverlayOptions_TooLong(t *testing.T) {
	var lowers []string
	for i := 0; i < 200; i++ {
		lowers = append(lowers, "/blobs/sha256/"+strings.Repeat("a", 32))
	}
	_, err := overlayOptions(lowers)
	if err == nil {
		t.Fatal("expected error for oversized option string")
	}
	if !strings.Contains(err.Error(), "exceeds kernel limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveAutoTmpfs_SkipsSymlinks(t *testing.T) {
	dst := t.TempDir()
	// /run here is a symlink — it must be skipped so a tmpfs is never mounted
	// through it onto an external path.
	if err := os.Symlink("/somewhere", filepath.Join(dst, "run")); err != nil {
		t.Fatal(err)
	}

	got := ResolveAutoTmpfs(dst, map[string]string{"/tmp": "256m"})

	for _, o := range got {
		if o.Target == filepath.Join(dst, "run") {
			t.Errorf("symlinked /run must be skipped, got %v", got)
		}
		if o.Target == filepath.Join(dst, "tmp") && o.Size != "256m" {
			t.Errorf("/tmp size = %q, want 256m", o.Size)
		}
	}
}

func TestAutoTmpfsPaths_NoVarRun(t *testing.T) {
	// /var/run is a symlink to /run on modern images; mounting it would escape
	// onto the host's /run, so it must not be in the default set.
	for _, p := range AutoTmpfsPaths {
		if p == "/var/run" {
			t.Errorf("/var/run must not be an auto-tmpfs path: %v", AutoTmpfsPaths)
		}
	}
}

func TestEnsureWithin_AllowsRealSubpath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0755); err != nil {
		t.Fatal(err)
	}
	// existing dir
	if err := EnsureWithin(root, filepath.Join(root, "run")); err != nil {
		t.Errorf("real subpath rejected: %v", err)
	}
	// not-yet-created leaf under an existing dir
	if err := EnsureWithin(root, filepath.Join(root, "var", "lib", "app")); err != nil {
		t.Errorf("non-existent subpath rejected: %v", err)
	}
}

func TestEnsureWithin_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// root/var/run -> outside (absolute), mimicking /var/run -> /run
	if err := os.MkdirAll(filepath.Join(root, "var"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "var", "run")); err != nil {
		t.Fatal(err)
	}

	if err := EnsureWithin(root, filepath.Join(root, "var", "run")); err == nil {
		t.Fatal("expected escape via symlink to be rejected")
	}
}
