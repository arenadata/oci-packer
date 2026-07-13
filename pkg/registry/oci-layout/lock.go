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
	"path/filepath"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// LockFile is the per-layout advisory lock file kept in the layout root next to
// oci-layout and index.json. A single OS-level exclusive lock on it serialises
// index/blob mutations of one layout (copy, delete) across processes so a
// delete's garbage collector cannot pull a shared layer out from under a
// concurrent copy, and the two index writers cannot clobber each other.
//
// The file is not part of the OCI image-layout spec; tools reading the layout
// ignore unknown entries. It is a persistent well-known marker — created on the
// first lock and left on disk (empty) afterwards to be reused.
const LockFile = "index.lock"

// lockPath returns the advisory lock file path for a layout rooted at dir.
func lockPath(dir string) string {
	return filepath.Join(dir, LockFile)
}

// Lock acquires an exclusive, cross-process advisory lock on the layout,
// blocking until it is available. The returned function releases the lock and
// must be called, typically via defer.
//
// The lock is backed by fcntl on Unix and LockFileEx on Windows, so it works on
// any OS and the kernel releases it automatically if the process exits or is
// killed while holding it — a crash cannot leave the layout locked. On Unix the
// lock tracks the file's inode, so two different relative paths to the same
// layout still exclude each other.
func (l Layout) Lock() (func(), error) {
	return lockedfile.MutexAt(lockPath(l.ref.Path)).Lock()
}
