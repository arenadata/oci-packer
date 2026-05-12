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
	"os"

	"github.com/arenadata/oci-packer"
	"github.com/arenadata/oci-packer/internal/version"
	packerhttp "github.com/arenadata/oci-packer/pkg/http"
	"github.com/arenadata/oci-packer/pkg/registry/remote"

	"github.com/containerd/log"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "oci-pack <reference>",
	Version: version.Version(),
	Short:   "Build manifests from pack-file and upload artifacts to container registry",
	PreRunE: cobra.ExactArgs(1),
	Run:     packRun,
}

func init() {
	log.L.Logger.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "20060102150405",
		FullTimestamp:   true,
	})

	rootCmd.PersistentFlags().StringP("login", "l", "", "Login to registry.")
	rootCmd.PersistentFlags().StringP("password", "p", "", "Password to use when connecting to registry.")
	rootCmd.PersistentFlags().Bool("plain-http", false, "Allow insecure connections to registry without TLS.")
	rootCmd.PersistentFlags().Bool("insecure", false, "Allow insecure TLS connections to the registry.")
	rootCmd.MarkFlagsMutuallyExclusive("plain-http", "insecure")

	rootCmd.Flags().StringP("file", "f", "", "Path to the pack file.")
	_ = rootCmd.MarkFlagRequired("file")

	rootCmd.Flags().String("tmp-dir", "", "Path to the temporary directory.")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func packRun(cmd *cobra.Command, args []string) {
	ref := args[0]
	file, _ := cmd.Flags().GetString("file")
	tmpDir, _ := cmd.Flags().GetString("tmp-dir")
	plainHttp, _ := cmd.Flags().GetBool("plain-http")
	login, _ := cmd.Flags().GetString("login")
	password, _ := cmd.Flags().GetString("password")

	packManifest, err := packer.LoadFromFile(file)
	if err != nil {
		log.L.Fatal(err)
	}

	var opts []remote.Option
	if plainHttp {
		opts = append(opts, remote.WithPlainHttp())
	}

	if len(login) > 0 {
		packerClient := packerhttp.New(packerhttp.WithAuthCreds(func(string) (string, string, error) {
			return login, password, nil
		}))
		opts = append(opts, remote.WithClient(packerClient))
	}

	repoClient, err := remote.New(ref, opts...)
	if err != nil {
		log.L.Fatal(err)
	}

	desc, err := packManifest.Pack(cmd.Context(), repoClient, packer.WithTmpDir(tmpDir))
	if err != nil {
		log.L.Fatal(err)
	}

	if err = repoClient.SetTag(cmd.Context(), desc); err != nil {
		log.L.Fatal(err)
	}
}
