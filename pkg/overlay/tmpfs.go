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
)

// AutoTmpfsPaths are volatile paths mounted as tmpfs by default so that
// applications writing to them do not hit EROFS on the read-only overlay.
//
// /var/run is intentionally absent: on modern images it is a symlink to /run,
// so mounting it would resolve to and shadow the host's real /run. /run already
// covers it. EnsureWithin guards against any remaining symlink escapes.
var AutoTmpfsPaths = []string{
	"/tmp",
	"/run",
	"/var/tmp",
}

// ResolveAutoTmpfs returns tmpfs targets under dst for each path in
// AutoTmpfsPaths, skipping symlinks (e.g. /var/run -> /run) to avoid
// double-mounting. The per-path size is taken from sizes, keyed by the
// original path (e.g. "/tmp"); paths absent from sizes get no limit.
func ResolveAutoTmpfs(dst string, sizes map[string]string) []TmpfsOptions {
	var out []TmpfsOptions
	for _, p := range AutoTmpfsPaths {
		target := filepath.Join(dst, p)
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		out = append(out, TmpfsOptions{Target: target, Size: sizes[p]})
	}
	return out
}
