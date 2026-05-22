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

package remote

import (
	"net/url"
	"path"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/opencontainers/go-digest"
)

type registryUrl struct {
	reference.Reference
	scheme string
}

func fromRef(ref reference.Reference, plainHttp bool) registryUrl {
	scheme := "https"
	if plainHttp {
		scheme = "http"
	}

	ref.Host = normalizeRegistry(ref.Host)
	if ref.Host == "index.docker.io" {
		if !strings.Contains(ref.Path, "/") {
			ref.Path = "library/" + ref.Path
		}
	}

	return registryUrl{
		Reference: ref,
		scheme:    scheme,
	}
}

func normalizeRegistry(registry string) string {
	switch registry {
	case "registry-1.docker.io", "docker.io":
		return "index.docker.io"
	}
	return registry
}

func (r registryUrl) v2() *url.URL {
	return &url.URL{Scheme: r.scheme, Host: r.Host, Path: "/v2/"}
}

func (r registryUrl) repo() *url.URL {
	u := r.v2()
	u.Path = path.Join(u.Path, r.Path)
	return u
}

func (r registryUrl) manifests() string {
	u := r.repo()
	u.Path = path.Join(u.Path, "manifests", r.Ref)
	return u.String()
}

func (r registryUrl) blobs() string {
	u := r.repo()
	u.Path = path.Join(u.Path, "blobs", r.Ref)
	return u.String()
}

func (r registryUrl) uploadsUrl() *url.URL {
	u := r.repo()
	u.Path = path.Join(u.Path, "blobs/uploads") + "/"
	return u
}

func (r registryUrl) uploads() string {
	return r.uploadsUrl().String()
}

func (r registryUrl) mount(mount digest.Digest, from string) string {
	val := make(url.Values)
	val.Set("mount", mount.String())
	val.Set("from", from)

	u := r.uploadsUrl()
	u.RawQuery = val.Encode()
	return u.String()
}
