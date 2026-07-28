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
	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/internal/version"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/remote"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "oci-pack <reference>",
	Version: version.Version(),
	Short:   "Build manifests from pack-file and upload artifacts to container registry",
	Args:    cobra.ExactArgs(1),
	Run:     packRun,
}

func init() {
	var verbose bool
	cobra.OnInitialize(func() {
		if verbose {
			logger.SetLevelDebug()
		}
	})
	// TODO
	//rootCmd.PersistentFlags().BoolVar(&showProgress, "progress", false, "Show progress output.")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output.")
	//rootCmd.MarkFlagsMutuallyExclusive("verbose", "progress")

	rootCmd.PersistentFlags().StringP("login", "l", "", "Login to registry.")
	rootCmd.PersistentFlags().StringP("password", "p", "", "Password to use when connecting to registry.")
	rootCmd.PersistentFlags().Bool("plain-http", false, "Allow insecure connections to registry without TLS.")
	rootCmd.PersistentFlags().Bool("insecure", false, "Allow insecure TLS connections to the registry.")
	rootCmd.MarkFlagsMutuallyExclusive("plain-http", "insecure")

	rootCmd.Flags().StringP("file", "f", "", "Path to the pack file.")
	_ = rootCmd.MarkFlagRequired("file")

	rootCmd.Flags().String("tmp-dir", "", "Path to the temporary directory.")
	rootCmd.Flags().IntP("parallel", "j", packer.DefaultConcurrency,
		"Number of sources to download and blobs to upload simultaneously (1 does them one at a time)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func packRun(cmd *cobra.Command, args []string) {
	log := logger.New("pack")
	ref := args[0]
	file, _ := cmd.Flags().GetString("file")
	tmpDir, _ := cmd.Flags().GetString("tmp-dir")

	parallel, _ := cmd.Flags().GetInt("parallel")
	if parallel < 1 {
		log.WithField("parallel", parallel).Fatal("--parallel must be at least 1")
	}

	log.WithFields(map[string]any{
		"version":   version.Version(),
		"pack_file": file,
		"tmp_dir":   tmpDir,
		"parallel":  parallel,
		"reference": ref,
	}).Debug("command configuration")

	packManifest, err := packer.LoadFromFile(file)
	if err != nil {
		log.WithError(err).WithField("pack_file", file).Fatal("failed to load pack file")
	}

	repoClient, err := remoteClientFromCommandArguments(cmd, ref)
	if err != nil {
		log.WithError(err).WithField("reference", ref).Fatal("failed to create remote registry client")
	}

	log.WithField("reference", ref).Info("remote registry client initialized")

	log.WithFields(map[string]any{
		"items_count": len(packManifest.Items),
		"reference":   ref,
	}).Debug("starting pack operation")

	desc, err := packManifest.Pack(cmd.Context(), repoClient,
		packer.WithTmpDir(tmpDir), packer.WithConcurrency(parallel))
	if err != nil {
		log.WithError(err).Fatal("pack operation failed")
	}

	log.WithFields(map[string]any{"digest": desc.Digest}).Debug("pack operation completed successfully")

	logFields := map[string]any{"reference": ref, "digest": desc.Digest}
	if err = repoClient.SetTag(cmd.Context(), desc); err != nil {
		log.WithError(err).WithFields(logFields).Fatal("failed to set tag to repository")
		return
	}

	log.WithFields(logFields).Debug("tag set successfully")
	log.WithField("reference", ref).Info("oci-packer execution completed")
}

func remoteClientFromCommandArguments(cmd *cobra.Command, ref string) (registry.Resolver, error) {
	log := logger.New("remote_client")

	plainHttp, _ := cmd.Flags().GetBool("plain-http")
	insecure, _ := cmd.Flags().GetBool("insecure")
	login, _ := cmd.Flags().GetString("login")
	password, _ := cmd.Flags().GetString("password")

	log.Debug(map[string]any{
		"login":      login,
		"plain-http": plainHttp,
		"insecure":   insecure,
	}, "configure remote client")

	opts := []remote.Option{remote.WithCreds(login, password)}
	if plainHttp {
		opts = append(opts, remote.WithPlainHttp())
	}
	if insecure {
		opts = append(opts, remote.WithInsecure())
	}

	return remote.New(ref, opts...)
}
