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
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/spf13/cobra"
)

var copyCmd = &cobra.Command{
	Use:   "copy <src> <dst>",
	Short: "Copy OCI Pack between remote registry and OCI layout",
	Args:  cobra.ExactArgs(2),
	Run:   copyCmdRun,
}

func init() {
	rootCmd.AddCommand(copyCmd)

	copyCmd.Flags().Bool("unpack", false, "Create OCI layout with unpack Layers")
}

func copyCmdRun(cmd *cobra.Command, args []string) {
	src, dst := args[0], args[1]

	log := logger.New("copy")
	log.WithFields(map[string]any{"src": src, "dst": dst}).Debug("determining source and destination references")

	var err error
	var srcRepo, dstRepo registry.Resolver
	var srcType, dstType string

	if reference.OciScheme.IsPrefix(src) {
		srcType, dstType = "OCI Layout", "Remote Registry"

		srcRepo, err = layout.New(src)
		if err != nil {
			log.WithError(err).WithField("src", src).Fatal("failed to create OCI layout source")
		}

		dstRepo, err = remoteClientFromCommandArguments(cmd, dst)
		if err != nil {
			log.WithError(err).WithField("dst", dst).Fatal("failed to create remote registry destination")
		}
	} else {
		srcType, dstType = "Remote Registry", "OCI Layout"

		srcRepo, err = remoteClientFromCommandArguments(cmd, src)
		if err != nil {
			log.WithError(err).WithField("src", src).Fatal("failed to create remote registry destination")
		}

		dstRepo, err = layout.New(dst)
		if err != nil {
			log.WithError(err).WithField("dst", dst).Fatal("failed to create OCI layout source")
		}
	}

	log.WithFields(map[string]any{"src_type": srcType, "dst_type": dstType}).Debug("resolvers initialized")

	desc, err := srcRepo.Resolve(cmd.Context(), "")
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to resolve source reference")
	}

	log.WithField("digest", desc.Digest).Info("source reference resolved")

	if err = registry.Copy(cmd.Context(), dstRepo, srcRepo, desc); err != nil {
		log.WithError(err).WithFields(map[string]any{"src": src, "dst": dst}).Fatal("copy operation failed")
	}

	if err = dstRepo.SetTag(cmd.Context(), desc); err != nil {
		log.WithError(err).WithFields(map[string]any{"dst": dst, "digest": desc.Digest}).
			Fatal("failed to set tag in destination")
	}

	log.Info(nil, "copy operation completed successfully")
}
