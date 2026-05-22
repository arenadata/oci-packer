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
	"strings"

	"github.com/opencontainers/go-digest"
)

type Schema string

func (s Schema) String() string { return fmt.Sprintf("%s://", string(s)) }

func (s Schema) IsPrefix(v string) bool {
	return strings.HasPrefix(v, s.String())
}

func (s Schema) Eq(v string) bool {
	return string(s) == v
}

const (
	OciScheme      Schema = "oci"
	RegistryScheme Schema = "cr"
	DirSchema      Schema = "dir"
	FileSchema     Schema = "file"
	S3Schema       Schema = "s3"
	S3httpSchema   Schema = "s3+http"
	HttpSchema     Schema = "http"
	HttpsSchema    Schema = "https"
)

var isRemoteScheme = map[Schema]bool{
	OciScheme:      false,
	DirSchema:      false,
	FileSchema:     false,
	RegistryScheme: true,
	S3Schema:       true,
	S3httpSchema:   true,
	HttpSchema:     true,
	HttpsSchema:    true,
}

var (
	// ErrInvalid is returned when there is an invalid reference
	ErrInvalid           = errors.New("invalid reference")
	ErrSchemeRequired    = errors.New("scheme required")
	ErrSchemeUnsupported = errors.New("scheme unsupported")

	// ErrHostnameRequired is returned when the hostname is required
	ErrHostnameRequired = errors.New("hostname required")
)

type Reference struct {
	Scheme Schema
	Host   string
	Path   string
	Ref    string
}

func Parse(s string) (Reference, error) {
	if len(s) == 0 {
		return Reference{}, ErrInvalid
	}

	urlParts := strings.SplitN(s, "://", 2)
	if len(urlParts) != 2 {
		return Reference{}, ErrSchemeRequired
	}

	scheme, imageRef := urlParts[0], urlParts[1]
	if len(scheme) == 0 {
		return Reference{}, ErrSchemeRequired
	}
	if !OciScheme.Eq(scheme) && !RegistryScheme.Eq(scheme) {
		return Reference{}, ErrSchemeUnsupported
	}

	return parsePath(scheme, imageRef)
}

func parsePath(scheme, imageRef string) (Reference, error) {
	var ref Reference

	if strings.Contains(imageRef, "//") {
		return ref, ErrInvalid
	}
	if isRemoteScheme[Schema(scheme)] {
		idx := strings.IndexByte(imageRef, '/')
		if idx == -1 {
			ref.Path = imageRef
		} else {
			ref.Host = imageRef[:idx]
			if len(ref.Host) == 0 {
				return ref, ErrHostnameRequired
			}
			imageRef = strings.TrimPrefix(imageRef[idx+1:], "/")
		}
	}

	ref.Scheme = Schema(scheme)

	idx := strings.IndexAny(imageRef, "@:")
	if idx == -1 {
		ref.Path, ref.Ref = imageRef, "latest"
	} else {
		ref.Path, ref.Ref = imageRef[:idx], imageRef[idx+1:]

		if imageRef[idx] == '@' {
			if _, err := digest.Parse(ref.Ref); err != nil {
				return Reference{}, ErrInvalid
			}
		}
	}

	if len(ref.Path) == 0 || strings.HasSuffix(ref.Path, "/") {
		return Reference{}, ErrInvalid
	}

	return ref, nil
}

func (r Reference) String() string {
	sep := ":"
	if _, err := digest.Parse(r.Ref); err == nil {
		sep = "@"
	}
	if OciScheme.Eq(string(r.Scheme)) {
		return strings.Join([]string{string(r.Scheme), r.Path, sep, r.Ref}, "")
	}
	return fmt.Sprintf("%s%s/%s%s%s", r.Scheme, r.Host, r.Path, sep, r.Ref)
}

func ParseRegistryReference(repoRef Reference, ref string) (Reference, error) {
	if len(ref) > 0 && !isDigest(ref) && strings.ContainsAny(ref, "@:") {
		// [cr://registry.host/[repo/]]image[:tag|@digest]
		var err error
		var parsedReference Reference
		if idx := strings.Index(ref, "://"); idx > -1 {
			if !RegistryScheme.IsPrefix(ref) {
				return Reference{}, ErrSchemeUnsupported
			}
			parsedReference, err = Parse(ref)
		} else {
			parsedReference, err = parsePath(string(RegistryScheme), ref)
		}
		if err != nil {
			return Reference{}, err
		}

		repoRef.Path = parsedReference.Path
		repoRef.Ref = parsedReference.Ref
		return repoRef, nil
	}

	if len(ref) > 0 {
		// tag or digest
		repoRef.Ref = ref
	}

	return repoRef, nil
}

func isDigest(s string) bool {
	return len(s) > 64 && s[:3] == "sha" && s[6] == ':'
}

func IsFile(from string) bool {
	return FileSchema.IsPrefix(from)
}
func IsDir(from string) bool {
	return DirSchema.IsPrefix(from)
}

func IsHTTP(from string) bool {
	return HttpsSchema.IsPrefix(from) || HttpSchema.IsPrefix(from)
}

func IsS3(from string) bool {
	return S3Schema.IsPrefix(from) || S3httpSchema.IsPrefix(from)
}

func IsOCI(from string) bool {
	return RegistryScheme.IsPrefix(from) || OciScheme.IsPrefix(from)
}
