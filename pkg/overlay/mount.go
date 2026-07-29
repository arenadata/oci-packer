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

// emptyLowerSuffix names the empty directory a single-layer mount uses as its bottom layer.
const emptyLowerSuffix = ".empty-lower"

// Mount mounts the lower layers read-only at opts.Target, always as overlayfs, with opts.Flags
// applied by the same syscall that creates the mount.
//
// A single layer gets an EMPTY directory as a second, bottom lower rather than being bind-mounted.
// overlayfs does reject one lowerdir with no upperdir ("at least 2 lowerdir are needed while
// upperdir nonexistent"), which is why the bind existed — but a bind cannot carry per-mount flags:
// the kernel ignores MS_NOSUID/MS_NODEV on the call that creates a bind and honours them only on a
// following MS_REMOUNT|MS_BIND, so the mount is briefly live without them. For image content that
// window is a local privilege-escalation race. An empty bottom layer costs one directory and makes
// both paths a single, atomic mount. Single-layer images (busybox, alpine, most minimal bases) take
// this path, so it is the common case, not an edge one.
func Mount(opts MountOptions) error {
	lowers := opts.LowerDirs
	if len(lowers) == 1 {
		empty := opts.Target + emptyLowerSuffix
		if err := os.MkdirAll(empty, 0755); err != nil {
			return err
		}
		// Bottom-to-top: the empty layer is the lowest, so the real layer wins every path.
		lowers = append([]string{empty}, lowers...)
	}

	data, err := overlayOptions(lowers) // rejects zero layers; reverses to overlay order
	if err != nil {
		return err
	}
	if err = os.MkdirAll(opts.Target, 0755); err != nil {
		return err
	}
	return syscall.Mount("overlay", opts.Target, "overlay", syscall.MS_RDONLY|opts.Flags, data)
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
	if err := syscall.Unmount(target, flags); err != nil {
		return err
	}
	// Reclaim the empty bottom layer a single-layer mount created. It is only ever empty, so
	// removing it can lose nothing, and it is absent for a multi-layer mount.
	_ = os.Remove(target + emptyLowerSuffix)
	return nil
}
