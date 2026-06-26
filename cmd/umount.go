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

package cmd

import (
	"path/filepath"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/overlay"
	"github.com/spf13/cobra"
)

var umountCmd = &cobra.Command{
	Use:   "umount <dst>",
	Short: "Unmount an OCI overlay mount and all associated bind/tmpfs mounts",
	Args:  cobra.ExactArgs(1),
	Run:   umountCmdRun,
}

func init() {
	rootCmd.AddCommand(umountCmd)
	umountCmd.Flags().Bool("lazy", false, "Use lazy unmount (MNT_DETACH) — detach even if busy")
}

func umountCmdRun(cmd *cobra.Command, args []string) {
	dst := filepath.Clean(args[0])
	lazy, _ := cmd.Flags().GetBool("lazy")
	log := logger.New("umount")

	// Children (deeper paths) are unmounted first so the overlay is not busy.
	targets, err := overlay.MountedUnder(dst)
	if err != nil {
		log.WithError(err).Fatal("failed to read mount table")
	}
	if len(targets) == 0 {
		log.WithField("target", dst).Info("nothing mounted under target")
		return
	}

	var failed int
	for _, target := range targets {
		if err = overlay.Unmount(target, lazy); err != nil {
			failed++
			log.WithError(err).WithField("target", target).Warn("failed to unmount, continuing")
			continue
		}
		log.WithField("target", target).Info("unmounted")
	}

	if failed > 0 {
		log.Fatalf("%d of %d mount(s) failed to unmount", failed, len(targets))
	}
	log.WithField("target", dst).Info("all mounts removed")
}
