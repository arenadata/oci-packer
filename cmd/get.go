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
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/extract"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

var getCmd = &cobra.Command{
	Use:   "get <oci://host/repo:tag:path/to/file> [dest]",
	Short: "Download one file of an artifact from a registry",
	Long: `Download one packed file of an artifact, without the rest of it: only the layer that holds
the file is fetched. The file is named after the tag (or the digest) with a second colon, by its
path in the artifact's tree — the title of the layer, under the directory a dir:// item named:

  oci-packer get oci://remote.registry.com/packs/application:v1.2.3:schema.json
  oci-packer get oci://remote.registry.com/packs/application:v1.2.3:templates/app.tmpl ./app.tmpl

It is written to dest, or to the standard output when there is none (or it is -). A tag or a
digest is required: it is what the second colon follows. 'oci-packer files' lists what an
artifact packs.`,
	Args: cobra.RangeArgs(1, 2),
	Run:  getCmdRun,
}

var filesCmd = &cobra.Command{
	Use:   "files <oci://host/repo:tag>",
	Short: "List the files an artifact packs",
	Long: `List the files an artifact packs itself, by their paths in its tree, with their sizes. A mounted
image or a mounted artifact is not one of them.`,
	Args: cobra.ExactArgs(1),
	Run:  filesCmdRun,
}

func init() {
	rootCmd.AddCommand(getCmd, filesCmd)
}

func getCmdRun(cmd *cobra.Command, args []string) {
	log := logger.New("get")
	ref, file, err := reference.ParseFile(args[0])
	if err != nil {
		log.WithError(err).Fatal("failed to parse reference")
	}
	src, err := remoteClientFromCommandArguments(cmd, ref.String())
	if err != nil {
		log.WithError(err).Fatal("failed to create the registry client")
	}
	_, r, err := extract.Open(cmd.Context(), src, reference.Reference{Ref: ref.Ref}, file, extract.Options{})
	if err != nil {
		log.WithError(err).Fatal("failed to get the file")
	}
	defer func() { _ = r.Close() }()

	var w io.Writer = os.Stdout
	if len(args) == 2 && args[1] != "-" {
		if err := os.MkdirAll(filepath.Dir(args[1]), 0o755); err != nil {
			log.WithError(err).Fatal("failed to make the directory")
		}
		f, err := os.Create(args[1])
		if err != nil {
			log.WithError(err).Fatal("failed to create the file")
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	if _, err := io.Copy(w, r); err != nil {
		log.WithError(err).Fatal("failed to write the file")
	}
}

func filesCmdRun(cmd *cobra.Command, args []string) {
	log := logger.New("files")
	ref, err := reference.Parse(args[0])
	if err != nil {
		log.WithError(err).Fatal("failed to parse reference")
	}
	src, err := remoteClientFromCommandArguments(cmd, ref.String())
	if err != nil {
		log.WithError(err).Fatal("failed to create the registry client")
	}
	files, err := extract.List(cmd.Context(), src, reference.Reference{Ref: ref.Ref}, extract.Options{})
	if err != nil {
		log.WithError(err).Fatal("failed to list the files")
	}
	for _, f := range files {
		fmt.Printf("%-48s %d\n", f.Path, f.Desc.Size)
	}
}
