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
	"strings"

	"github.com/opencontainers/go-digest"
)

var (
	// ErrInvalid is returned when there is an invalid reference
	ErrInvalid           = errors.New("invalid reference")
	ErrSchemeRequired    = errors.New("scheme required")
	ErrSchemeUnsupported = errors.New("scheme unsupported")

	// ErrHostnameRequired is returned when the hostname is required
	ErrHostnameRequired = errors.New("hostname required")
)

func Parse(s string) (Reference, error) {
	scheme, imagePathWithRef, err := splitScheme(s)
	if err != nil {
		return Reference{}, err
	}

	if !OciScheme.Eq(scheme) && !RegistryScheme.Eq(scheme) {
		return Reference{}, ErrSchemeUnsupported
	}

	return parse(scheme, imagePathWithRef)
}

func splitScheme(s string) (string, string, error) {
	if len(s) == 0 {
		return "", "", ErrInvalid
	}

	urlParts := strings.SplitN(s, "://", 2)
	if len(urlParts) != 2 {
		return "", "", ErrSchemeRequired
	}

	scheme, imagePathWithRef := urlParts[0], urlParts[1]
	if len(scheme) == 0 {
		return "", "", ErrSchemeRequired
	}

	if _, ok := isRemoteScheme[Schema(scheme)]; !ok {
		return "", "", ErrSchemeUnsupported
	}

	return scheme, imagePathWithRef, nil
}

func parse(scheme, imagePathWithRef string) (Reference, error) {
	var ref Reference

	if strings.Contains(imagePathWithRef, "//") {
		return ref, ErrInvalid
	}

	if RegistryScheme.Eq(scheme) {
		idx := strings.IndexByte(imagePathWithRef, '/')
		if idx == -1 {
			ref.Path = imagePathWithRef
		} else {
			ref.Host = imagePathWithRef[:idx]
			if len(ref.Host) == 0 {
				return ref, ErrHostnameRequired
			}
			imagePathWithRef = strings.TrimPrefix(imagePathWithRef[idx+1:], "/")
		}
	}

	ref.Scheme = Schema(scheme)

	idx := strings.IndexAny(imagePathWithRef, "@:")
	if idx == -1 {
		ref.Path, ref.Ref = imagePathWithRef, "latest"
	} else {
		ref.Path, ref.Ref = imagePathWithRef[:idx], imagePathWithRef[idx+1:]

		if imagePathWithRef[idx] == '@' {
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

func ParseRegistryReference(ref string) (Reference, error) {
	if len(ref) > 0 && !isDigest(ref) && strings.ContainsAny(ref, "@:") {
		// [cr://registry.host/[repo/]]image[:tag|@digest]
		var err error
		var parsedReference Reference
		if idx := strings.Index(ref, "://"); idx > -1 {
			if !RegistryScheme.IsPrefix(ref) && !OciScheme.IsPrefix(ref) {
				return Reference{}, ErrSchemeUnsupported
			}
			parsedReference, err = Parse(ref)
		} else {
			parsedReference, err = parse(string(RegistryScheme), ref)
		}
		if err != nil {
			return Reference{}, err
		}

		return parsedReference, nil
	}

	var repoRef Reference
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
