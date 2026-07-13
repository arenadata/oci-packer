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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLock_AcquireCreatesLockFileAndReleases(t *testing.T) {
	dir := t.TempDir()
	l := &Layout{ref: makeRef(dir)}

	unlock, err := l.Lock()
	if err != nil {
		t.Fatalf("Lock() error: %v", err)
	}

	// The lock is materialised as a well-known file in the layout root.
	lockFile := filepath.Join(dir, LockFile)
	if _, err = os.Stat(lockFile); err != nil {
		t.Fatalf("lock file %q missing while held: %v", lockFile, err)
	}

	// Releasing must not error and the file is intentionally left behind for reuse.
	unlock()
	if _, err = os.Stat(lockFile); err != nil {
		t.Fatalf("lock file %q should persist after unlock: %v", lockFile, err)
	}

	// The lock is re-acquirable after release (fresh Mutex, same path/inode).
	unlock2, err := l.Lock()
	if err != nil {
		t.Fatalf("re-Lock() error: %v", err)
	}
	unlock2()
}

func TestLockPath(t *testing.T) {
	if got, want := lockPath("/some/layout"), filepath.Join("/some/layout", LockFile); got != want {
		t.Fatalf("lockPath = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// cross-process mutual-exclusion test
//
// fcntl/LockFileEx locks are held per process, so two goroutines in one process
// cannot exercise cross-process exclusion (on Unix a process never conflicts
// with its own locks). We instead re-exec the test binary as a second process:
// the child grabs the lock and holds it, and the parent proves that its own
// Lock() blocks until the child releases.
// ---------------------------------------------------------------------------

// lockHelperEnvVar carries the layout directory to the re-executed child and,
// by being set, selects the child branch of TestLock_CrossProcessMutualExclusion.
const lockHelperEnvVar = "OCI_PACKER_LOCK_HELPER_DIR"

// lockHelperHold is how long the child keeps the lock after announcing it. It
// must comfortably exceed lockHelperMinBlock so the parent is still blocked when
// it measures.
const lockHelperHold = time.Second

// lockHelperMinBlock is the smallest wait that still proves the parent was
// blocked by the child rather than acquiring immediately. Generously below
// lockHelperHold to tolerate scheduling jitter on busy CI.
const lockHelperMinBlock = 300 * time.Millisecond

// lockHelperReady is printed to stdout by the child once it holds the lock.
const lockHelperReady = "HELPER_LOCKED"

func TestLock_CrossProcessMutualExclusion(t *testing.T) {
	// Child branch: acquire the lock, tell the parent, hold, release, exit.
	if dir := os.Getenv(lockHelperEnvVar); dir != "" {
		runLockHelper(dir)
		return // unreachable: runLockHelper calls os.Exit
	}

	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Re-exec this very test in a child process, gated into the child branch by
	// lockHelperEnvVar so it does not spawn a grandchild.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLock_CrossProcessMutualExclusion$")
	cmd.Env = append(os.Environ(), lockHelperEnvVar+"="+dir)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Block until the child reports that it holds the lock.
	waitForLine(t, stdout, lockHelperReady)
	heldSince := time.Now()

	// The child is holding the lock; our acquisition must not return until it
	// releases lockHelperHold later.
	l := &Layout{ref: makeRef(dir)}
	unlock, err := l.Lock()
	if err != nil {
		t.Fatalf("parent Lock(): %v", err)
	}
	blocked := time.Since(heldSince)
	unlock()

	if err = cmd.Wait(); err != nil {
		t.Fatalf("helper process exited with error: %v", err)
	}

	if blocked < lockHelperMinBlock {
		t.Fatalf("parent acquired the layout lock after only %s while another process held it "+
			"for ~%s: the lock did not exclude across processes", blocked, lockHelperHold)
	}
	t.Logf("parent blocked %s waiting for the cross-process lock (child held ~%s)", blocked, lockHelperHold)
}

// runLockHelper is the child half of TestLock_CrossProcessMutualExclusion. It
// acquires the layout lock, announces it on stdout, holds it for lockHelperHold,
// then releases and exits. It reports via exit code and raw stdout/stderr rather
// than *testing.T so its output does not tangle with the test framework's.
func runLockHelper(dir string) {
	l := &Layout{ref: makeRef(dir)}
	unlock, err := l.Lock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lock helper: acquire: %v\n", err)
		os.Exit(1)
	}

	// os.Stdout is unbuffered, so this line reaches the parent immediately.
	fmt.Println(lockHelperReady)
	time.Sleep(lockHelperHold)

	unlock()
	os.Exit(0)
}

// waitForLine reads r until it sees a line equal to want, failing the test if
// the stream closes first or nothing arrives within the timeout.
func waitForLine(t *testing.T, r io.Reader, want string) {
	t.Helper()

	found := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if sc.Text() == want {
				found <- true
				return
			}
		}
		found <- false
	}()

	select {
	case ok := <-found:
		if !ok {
			t.Fatalf("helper stdout closed before printing %q", want)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for helper to print %q", want)
	}
}
