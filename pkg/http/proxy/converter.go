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

package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type RequestToReferenceConverter func(*http.Request) (reference.Reference, map[string]string, error)

// defaultConverter converts /request/url/path/reference into /request/url/path with a reference
// to request the container registry
func defaultConverter(r *http.Request) (reference.Reference, map[string]string, error) {
	idx := strings.LastIndexByte(r.URL.Path[1:], '/')
	if idx == -1 {
		return reference.Reference{}, nil, fmt.Errorf("invalid path")
	}
	path := r.URL.Path[:idx+1]
	ref := r.URL.Path[idx+2:]

	q := r.URL.Query()
	title := q.Get("title")
	if len(title) == 0 {
		return reference.Reference{}, nil, errors.New("title is required")
	}

	opts := map[string]string{ocispecv1.AnnotationTitle: title}

	if platform := q.Get("platform"); len(platform) > 0 {
		opts["platform"] = platform
	}

	return reference.Reference{
		Path: path,
		Ref:  ref,
	}, opts, nil
}
