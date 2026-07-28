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
	"github.com/containerd/platforms"
	"github.com/spf13/cobra"
)

var copyCmd = &cobra.Command{
	Use:   "copy <src> <dst>",
	Short: "Copy OCI Pack between remote registries and OCI layouts",
	Long: `Copy images/artifacts between a remote registry (cr://) and/or an OCI layout (oci://).

Any combination of endpoints is supported, including layout-to-layout. With --unpack the
destination layout stores layers as unpacked directories, so copying an existing tar-mode
layout into a new one with --unpack repacks it for 'oci-packer mount':

    oci-packer copy --unpack oci://./layout:app:v1 oci://./unpacked:app:v1

Layers are transferred in parallel, -j at a time. A manifest is written only once every blob
it references has arrived, and each digest is transferred once even when several manifests of
a multi-platform index share it.`,
	Args: cobra.ExactArgs(2),
	Run:  copyCmdRun,
}

func init() {
	rootCmd.AddCommand(copyCmd)

	copyCmd.Flags().Bool("unpack", false, "Store layers unpacked in the destination OCI layout")
	copyCmd.Flags().String("platform", "", "Copy only the given platform from a multi-platform image, e.g. linux/amd64")
	copyCmd.Flags().IntP("parallel", "j", registry.DefaultConcurrency,
		"Number of layers to copy simultaneously (1 copies them one at a time)")
}

func copyCmdRun(cmd *cobra.Command, args []string) {
	src, dst := args[0], args[1]

	log := logger.New("copy")
	log.WithFields(map[string]any{"src": src, "dst": dst}).Debug("determining source and destination references")

	unpack, _ := cmd.Flags().GetBool("unpack")

	parallel, _ := cmd.Flags().GetInt("parallel")
	if parallel < 1 {
		log.WithField("parallel", parallel).Fatal("--parallel must be at least 1")
	}

	// --unpack describes how the destination stores layers; the source is read
	// in whatever mode it already is (an existing layout reports its own mode).
	srcRepo, srcType, err := newCopyEndpoint(cmd, src, false)
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to create copy source")
	}

	dstRepo, dstType, err := newCopyEndpoint(cmd, dst, unpack)
	if err != nil {
		log.WithError(err).WithField("dst", dst).Fatal("failed to create copy destination")
	}

	log.WithFields(map[string]any{"src_type": srcType, "dst_type": dstType}).Debug("resolvers initialized")

	desc, err := srcRepo.Resolve(cmd.Context(), reference.Reference{})
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to resolve source reference")
	}

	log.WithField("digest", desc.Digest).Info("source reference resolved")

	if platform, _ := cmd.Flags().GetString("platform"); platform != "" {
		p, err := platforms.Parse(platform)
		if err != nil {
			log.WithError(err).WithField("platform", platform).Fatal("invalid platform")
		}

		desc, err = registry.SelectPlatform(cmd.Context(), srcRepo, desc, platforms.Only(p))
		if err != nil {
			log.WithError(err).WithField("platform", platform).Fatal("failed to select platform")
		}
		log.WithFields(map[string]any{"platform": platforms.Format(p), "digest": desc.Digest}).
			Info("selected platform manifest")
	}

	// Hold an exclusive cross-process lock across the whole push + tag sequence
	// when the destination is a local layout, so a concurrent 'delete' can't GC a
	// shared layer we rely on and the two index writes can't clobber each other.
	// Remote registries manage their own consistency and expose no Lock.
	if locker, ok := dstRepo.(interface{ Lock() (func(), error) }); ok {
		unlock, err := locker.Lock()
		if err != nil {
			log.WithError(err).WithField("dst", dst).Fatal("failed to lock destination layout")
		}
		defer unlock()
	}

	log.WithField("parallel", parallel).Info("copying")

	if err = registry.Copy(cmd.Context(), dstRepo, srcRepo, desc, registry.WithConcurrency(parallel)); err != nil {
		log.WithError(err).WithFields(map[string]any{"src": src, "dst": dst}).Fatal("copy operation failed")
	}

	if err = dstRepo.SetTag(cmd.Context(), desc); err != nil {
		log.WithError(err).WithFields(map[string]any{"dst": dst, "digest": desc.Digest}).
			Fatal("failed to set tag in destination")
	}

	log.Info("copy operation completed successfully")
}

// newCopyEndpoint builds a resolver for a copy endpoint based on its scheme:
// oci:// is an OCI layout (unpack applies when it is the destination), anything
// else is a remote registry.
func newCopyEndpoint(cmd *cobra.Command, ref string, unpack bool) (registry.Resolver, string, error) {
	if reference.OciScheme.IsPrefix(ref) {
		var opts []layout.Option
		if unpack {
			opts = append(opts, layout.Unpack())
		}
		r, err := layout.New(ref, opts...)
		return r, "OCI Layout", err
	}

	r, err := remoteClientFromCommandArguments(cmd, ref)
	return r, "Remote Registry", err
}
