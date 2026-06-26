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

// Package overlay mounts unpacked OCI layout layers read-only via Linux
// overlayfs, with writable tmpfs/bind mounts on top, either directly through
// syscalls or by generating systemd.mount units.
package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// lowerdirMaxBytes is a conservative cap on the overlayfs option string.
	// The kernel limits mount options to one page (~4096 bytes); we leave
	// headroom for the trailing flags.
	lowerdirMaxBytes = 3072
	kernelPageBytes  = 4096
)

// MountOptions describes a read-only overlay mount built from unpacked layers.
type MountOptions struct {
	LowerDirs []string // bottom-to-top, as recorded in the manifest
	Target    string
}

// BindOptions describes a bind mount of a host directory onto the target tree.
type BindOptions struct {
	Source string
	Target string // absolute path, already joined with the mount point
}

// TmpfsOptions describes a tmpfs mounted onto the target tree.
type TmpfsOptions struct {
	Target string
	Size   string // "size=" value, e.g. "256m"; empty means no limit
}

// overlayOptions builds the overlayfs option string. LowerDirs is given
// bottom-to-top and reversed here to overlayfs' top-to-bottom order. It returns
// an error if the option string would exceed the kernel page-size limit.
func overlayOptions(lowerDirs []string) (string, error) {
	if len(lowerDirs) == 0 {
		return "", fmt.Errorf("no layers to mount")
	}

	rev := make([]string, len(lowerDirs))
	for i, d := range lowerDirs {
		rev[len(lowerDirs)-1-i] = d
	}

	opts := "lowerdir=" + strings.Join(rev, ":") + ",ro"
	if len(opts) > lowerdirMaxBytes {
		return "", fmt.Errorf("lowerdir option string is %d bytes, exceeds kernel limit of ~%d bytes "+
			"(image has too many layers; consider squashing with 'oci-packer copy')", len(opts), kernelPageBytes)
	}
	return opts, nil
}

// EnsureWithin verifies that target, with every symlink resolved, stays inside
// root. It guards against a mount target escaping the overlay through a symlink
// — for example an image's /var/run -> /run, which would otherwise land a
// mount on the host's real /run. The mount must be performed only after this
// returns nil, and after the overlay is mounted so symlinks resolve against it.
func EnsureWithin(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve root %q: %w", root, err)
	}

	// Resolve the longest existing prefix of target, then re-append the
	// remaining (not-yet-created) suffix and check containment.
	probe := filepath.Clean(target)
	suffix := ""
	for {
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			full := filepath.Join(real, suffix)
			if full != realRoot && !strings.HasPrefix(full, realRoot+string(os.PathSeparator)) {
				return fmt.Errorf("target %q resolves to %q, outside overlay root %q", target, full, realRoot)
			}
			return nil
		}
		suffix = filepath.Join(filepath.Base(probe), suffix)
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("cannot resolve any ancestor of %q", target)
		}
		probe = parent
	}
}
