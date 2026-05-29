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
	"fmt"
	"strings"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type Reference struct {
	Scheme Schema
	Host   string
	Path   string
	Ref    string

	desc ocispecv1.Descriptor
}

func (r Reference) IsZero() bool {
	return r.Scheme == "" && r.Host == "" && r.Path == "" && r.Ref == ""
}

func (r Reference) String() string {
	if OciScheme.Eq(string(r.Scheme)) {
		return strings.Join([]string{r.Scheme.String(), r.RepoReference()}, "")
	}

	return fmt.Sprintf("%s%s/%s", r.Scheme, r.Host, r.RepoReference())
}

func (r Reference) Descriptor() ocispecv1.Descriptor {
	return r.desc
}

func (r Reference) WithDescriptor(desc ocispecv1.Descriptor) Reference {
	dst := r
	dst.desc = desc
	return dst
}

func (r Reference) RepoReference() string {
	sep := ":"
	if isDigest(r.Ref) {
		sep = "@"
	}
	return fmt.Sprintf("%s%s%s", r.Path, sep, r.Ref)
}

func (r Reference) Merge(ref Reference) Reference {
	dst := r
	if len(ref.Path) > 0 {
		dst.Path = ref.Path
	}
	if len(ref.Ref) > 0 {
		dst.Ref = ref.Ref
	}
	return dst
}

func (r Reference) URL(plainHttp bool) Url {
	return fromRef(r, plainHttp)
}
