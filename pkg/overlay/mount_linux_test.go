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

package overlay

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("overlay mount requires root")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestMount_ReadOnly(t *testing.T) {
	requireRoot(t)

	base := t.TempDir()
	lower1 := filepath.Join(base, "l1")
	lower2 := filepath.Join(base, "l2")
	target := filepath.Join(base, "merged")
	writeFile(t, filepath.Join(lower1, "a.txt"), "from-l1")
	writeFile(t, filepath.Join(lower2, "b.txt"), "from-l2")

	if err := Mount(MountOptions{LowerDirs: []string{lower1, lower2}, Target: target}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	defer func() { _ = Unmount(target, true) }()

	// Both layers visible in the merged view.
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("expected %s in merged view: %v", f, err)
		}
	}

	// Writes must fail with EROFS.
	err := os.WriteFile(filepath.Join(target, "new.txt"), []byte("x"), 0644)
	if !errors.Is(err, syscall.EROFS) {
		t.Errorf("expected EROFS writing to read-only overlay, got %v", err)
	}
}

func TestMountTmpfs_Writable(t *testing.T) {
	requireRoot(t)

	target := filepath.Join(t.TempDir(), "tmp")
	if err := MountTmpfs(TmpfsOptions{Target: target, Size: "16m"}); err != nil {
		t.Fatalf("MountTmpfs: %v", err)
	}
	defer func() { _ = Unmount(target, true) }()

	if err := os.WriteFile(filepath.Join(target, "scratch"), []byte("ok"), 0644); err != nil {
		t.Errorf("tmpfs should be writable: %v", err)
	}
}

func TestUmount_ChildrenFirst(t *testing.T) {
	requireRoot(t)

	base := t.TempDir()
	lower := filepath.Join(base, "l1")
	target := filepath.Join(base, "merged")
	writeFile(t, filepath.Join(lower, "a.txt"), "x")

	if err := Mount(MountOptions{LowerDirs: []string{lower}, Target: target}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := MountTmpfs(TmpfsOptions{Target: filepath.Join(target, "tmp")}); err != nil {
		_ = Unmount(target, true)
		t.Fatalf("MountTmpfs: %v", err)
	}

	targets, err := MountedUnder(target)
	if err != nil {
		t.Fatalf("MountedUnder: %v", err)
	}
	if len(targets) < 2 {
		t.Fatalf("expected overlay + tmpfs, got %v", targets)
	}
	// Deepest path (the tmpfs child) must come first.
	if targets[0] != filepath.Join(target, "tmp") {
		t.Errorf("expected child first, got %v", targets)
	}

	var failed int
	for _, mp := range targets {
		if err := Unmount(mp, true); err != nil {
			failed++
		}
	}
	if failed != 0 {
		t.Errorf("%d mounts failed to unmount", failed)
	}
}
