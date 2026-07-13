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

package layout

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
)

// mountedOrphans returns the subset of orphan blobs whose on-disk path is
// currently in use as an overlayfs lower directory.
func (l Layout) mountedOrphans(orphans []digest.Digest) ([]digest.Digest, error) {
	lowers, err := mountedLowerDirs()
	if err != nil {
		return nil, err
	}
	if len(lowers) == 0 {
		return nil, nil
	}

	var mounted []digest.Digest
	for _, d := range orphans {
		if _, ok := lowers[resolvePath(l.getBlobPath(d))]; ok {
			mounted = append(mounted, d)
		}
	}
	return mounted, nil
}

// mountedLowerDirs reads /proc/mounts and returns the set of directories
// currently used as overlayfs lower directories, keyed by their resolved
// absolute path.
func mountedLowerDirs() (map[string]struct{}, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	dirs := make(map[string]struct{})
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// fields: source target fstype options ...
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[2] != "overlay" {
			continue
		}
		for _, opt := range strings.Split(fields[3], ",") {
			val, ok := strings.CutPrefix(opt, "lowerdir=")
			if !ok {
				continue
			}
			for _, d := range strings.Split(val, ":") {
				dirs[resolvePath(unescapeMountField(d))] = struct{}{}
			}
		}
	}
	return dirs, sc.Err()
}

// resolvePath renders p as an absolute, symlink-resolved path for comparison,
// falling back to a cleaned path when it cannot be resolved (e.g. the blob does
// not exist on disk).
func resolvePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// unescapeMountField decodes the octal escapes (\040, \011, \012, \134) that
// /proc/mounts uses for spaces, tabs, newlines and backslashes.
func unescapeMountField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseInt(s[i+1:i+4], 8, 16); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
