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

package reference

import (
	"net/url"
	"path"
	"strings"

	"github.com/opencontainers/go-digest"
)

type Url struct {
	ref    Reference
	scheme string
}

func fromRef(ref Reference, plainHttp bool) Url {
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

	return Url{
		ref:    ref,
		scheme: scheme,
	}
}

func normalizeRegistry(registry string) string {
	switch registry {
	case "registry-1.docker.io", "docker.io":
		return "index.docker.io"
	}
	return registry
}

func (r Url) Path() string {
	return r.ref.Path
}

func (r Url) v2() *url.URL {
	return &url.URL{Scheme: r.scheme, Host: r.ref.Host, Path: "/v2/"}
}

func (r Url) repo() *url.URL {
	u := r.v2()
	u.Path = path.Join(u.Path, r.ref.Path)
	return u
}

func (r Url) Manifests() string {
	u := r.repo()
	u.Path = path.Join(u.Path, "manifests", r.ref.Ref)
	return u.String()
}

func (r Url) Blobs() string {
	u := r.repo()
	u.Path = path.Join(u.Path, "blobs", r.ref.Ref)
	return u.String()
}

func (r Url) UploadsUrl() *url.URL {
	u := r.repo()
	u.Path = path.Join(u.Path, "blobs/uploads") + "/"
	return u
}

func (r Url) Uploads() string {
	return r.UploadsUrl().String()
}

func (r Url) Mount(mount digest.Digest, from string) string {
	val := make(url.Values)
	val.Set("mount", mount.String())
	val.Set("from", from)

	u := r.UploadsUrl()
	u.RawQuery = val.Encode()
	return u.String()
}
