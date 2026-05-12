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
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/opencontainers/go-digest"
)

var (
	// ErrInvalid is returned when there is an invalid reference
	ErrInvalid = errors.New("invalid reference")
	// ErrHostnameRequired is returned when the hostname is required
	ErrHostnameRequired = errors.New("hostname required")
)

type Reference struct {
	Host  string
	Image string
	Ref   string
}

func Parse(s string) (Reference, error) {
	var ref Reference
	if strings.Contains(s, "//") {
		return ref, ErrInvalid
	}

	u, err := url.Parse("dummy://" + s)
	if err != nil {
		return ref, errors.Join(ErrInvalid, err)
	}

	if len(u.Host) == 0 {
		return ref, ErrHostnameRequired
	}

	ref.Host = u.Host

	imageRef, err := ParseImage(u.Path)
	if err != nil {
		return imageRef, err
	}

	ref.Image = imageRef.Image
	ref.Ref = imageRef.Ref

	return ref, nil
}

func ParseImage(s string) (Reference, error) {
	var ref Reference
	if len(s) == 0 {
		return ref, ErrInvalid
	}
	if n := strings.Count(s, "@"); n > 1 {
		return ref, ErrInvalid
	}
	if n := strings.Count(s, ":"); n > 1 {
		return ref, ErrInvalid
	}

	idx := strings.IndexAny(s, "@:")
	if idx == -1 {
		ref.Image, ref.Ref = s, "latest"
	} else {
		ref.Image, ref.Ref = s[:idx], s[idx+1:]

		if s[idx] == '@' {
			if _, err := digest.Parse(ref.Ref); err != nil {
				return Reference{}, ErrInvalid
			}
		}
	}
	ref.Image = strings.TrimPrefix(ref.Image, "/")

	if len(ref.Image) == 0 || strings.HasSuffix(ref.Image, "/") {
		return Reference{}, ErrInvalid
	}

	return ref, nil
}

func (r Reference) String() string {
	sep := ":"
	if _, err := digest.Parse(r.Ref); err == nil {
		sep = "@"
	}
	return fmt.Sprintf("%s/%s%s%s", r.Host, r.Image, sep, r.Ref)
}
