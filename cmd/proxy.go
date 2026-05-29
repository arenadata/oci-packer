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
	"log"
	"net/http"

	"github.com/arenadata/oci-packer/pkg/http/proxy"
	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy <registry>",
	Short: "HTTP proxy to OCI artifacts (Beta)",
	Args:  cobra.ExactArgs(1),
	Run:   proxyCmdRun,
}

func init() {
	rootCmd.AddCommand(proxyCmd)

	proxyCmd.Flags().Bool("unpack", false, "Use OCI layout with unpack Layers")
}

func proxyCmdRun(cmd *cobra.Command, args []string) {
	var opts []proxy.Option

	if ok, _ := cmd.Flags().GetBool("plain-http"); ok {
		opts = append(opts, proxy.WithRemotePlainHttp())
	}
	if ok, _ := cmd.Flags().GetBool("insecure"); ok {
		opts = append(opts, proxy.WithRemoteInsecure())
	}
	if ok, _ := cmd.Flags().GetBool("unpack"); ok {
		opts = append(opts, proxy.WithLayoutUnpack())
	}

	login, _ := cmd.Flags().GetString("login")
	password, _ := cmd.Flags().GetString("password")
	opts = append(opts, proxy.WithCreds(login, password))

	proxyHandler, err := proxy.New(args[0], opts...)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: ":8080", Handler: proxyHandler}
	log.Fatal(srv.ListenAndServe())
}
