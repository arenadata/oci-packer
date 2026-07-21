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
	"os"
	"syscall"
)

// Mount mounts the lower layers read-only at opts.Target. With two or more layers it
// uses overlayfs; a single layer is mounted as a read-only bind — overlayfs rejects one
// lowerdir with no upperdir ("at least 2 lowerdir are needed while upperdir nonexistent"),
// and a lone read-only layer is exactly a read-only view of that directory. Single-layer
// images (busybox, alpine, most minimal bases) take this path.
func Mount(opts MountOptions) error {
	if len(opts.LowerDirs) == 1 {
		if err := os.MkdirAll(opts.Target, 0755); err != nil {
			return err
		}
		if err := syscall.Mount(opts.LowerDirs[0], opts.Target, "", syscall.MS_BIND, ""); err != nil {
			return err
		}
		// A plain bind inherits the source's writability; a second bind+remount makes the
		// mount point read-only (the atomic-RO remount idiom, portable across kernels).
		if err := syscall.Mount("", opts.Target, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
			_ = syscall.Unmount(opts.Target, syscall.MNT_DETACH)
			return err
		}
		return nil
	}

	data, err := overlayOptions(opts.LowerDirs) // rejects zero layers; reverses to overlay order
	if err != nil {
		return err
	}
	if err = os.MkdirAll(opts.Target, 0755); err != nil {
		return err
	}
	return syscall.Mount("overlay", opts.Target, "overlay", syscall.MS_RDONLY, data)
}

// BindMount bind-mounts opts.Source onto opts.Target.
func BindMount(opts BindOptions) error {
	if err := os.MkdirAll(opts.Target, 0755); err != nil {
		return err
	}
	return syscall.Mount(opts.Source, opts.Target, "", syscall.MS_BIND, "")
}

// MountTmpfs mounts a writable tmpfs at opts.Target.
func MountTmpfs(opts TmpfsOptions) error {
	if err := os.MkdirAll(opts.Target, 0755); err != nil {
		return err
	}
	var data string
	if opts.Size != "" {
		data = "size=" + opts.Size
	}
	return syscall.Mount("tmpfs", opts.Target, "tmpfs", 0, data)
}

// Unmount unmounts target. If lazy is set, MNT_DETACH detaches a busy mount.
func Unmount(target string, lazy bool) error {
	flags := 0
	if lazy {
		flags = syscall.MNT_DETACH
	}
	return syscall.Unmount(target, flags)
}
