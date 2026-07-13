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
	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:     "delete <oci://layout:repo:tag>",
	Aliases: []string{"rm", "remove"},
	Short:   "Delete an image from an OCI layout, garbage-collecting unshared blobs",
	Long: `Delete an image (or artifact) from an OCI layout.

A layout may hold many images, so the reference selects one:

    oci://<layout-dir>:<repository>:<tag>
    e.g. oci://./layout:example/service:v1

The image's entry is removed from the index and every blob reachable only from
it is deleted. Layers shared with another image in the layout — or the same
manifest tagged under a second name — are kept.

In unpack mode a layer that is currently overlay-mounted is never removed: the
command aborts and lists the mounted layers so you can 'oci-packer umount'
them first.`,
	Args: cobra.ExactArgs(1),
	Run:  deleteCmdRun,
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}

func deleteCmdRun(cmd *cobra.Command, args []string) {
	src := args[0]
	log := logger.New("delete")

	parsedRef, err := reference.Parse(src)
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to parse reference")
	}

	resolver, err := layout.Open(parsedRef)
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to open OCI layout")
	}
	l, ok := resolver.(*layout.Layout)
	if !ok {
		log.Fatal("reference is not an OCI layout")
	}

	if err = l.Delete(cmd.Context(), reference.Reference{}); err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to delete image")
	}

	log.WithField("ref", parsedRef.Ref).Info("image deleted from layout")
}
