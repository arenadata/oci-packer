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

// Mount mounts the lower layers read-only at opts.Target via overlayfs.
func Mount(opts MountOptions) error {
	data, err := overlayOptions(opts.LowerDirs)
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
