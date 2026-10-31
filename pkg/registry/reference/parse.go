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
	"path"
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

	if !OciScheme.Eq(scheme) {
		return Reference{}, ErrSchemeUnsupported
	}

	ref, _, err := parse(!isLayout(imagePathWithRef), imagePathWithRef, false)
	return ref, err
}

// ErrFileRequired is returned when a file reference names no file.
var ErrFileRequired = errors.New("file reference: a path after the tag")

// ParseFile reads the reference to one file of an artifact in a registry:
//
//	oci://<host>[:<port>]/<repo>:<tag>:<path/to/file>
//	oci://<host>[:<port>]/<repo>@<algo>:<hex>:<path/to/file>
//
// The tag (or the digest) is required: the path follows it after a second
// colon, and may hold colons itself. The reference is the artifact's, with the
// file cut off. A layout is not named this way: its form already uses the colons
// — read a file of a layout with the path as a separate argument.
func ParseFile(s string) (Reference, string, error) {
	scheme, rest, err := splitScheme(s)
	if err != nil {
		return Reference{}, "", err
	}
	if !OciScheme.Eq(scheme) {
		return Reference{}, "", ErrSchemeUnsupported
	}
	if isLayout(rest) {
		return Reference{}, "", fmt.Errorf("%s: a file is named in a registry reference, oci://<host>/<repo>:<tag>:<path>", s)
	}
	ref, file, err := parse(true, rest, true)
	if err != nil {
		return Reference{}, "", err
	}
	if len(ref.Host) == 0 {
		return Reference{}, "", ErrInvalid
	}
	if len(file) == 0 {
		return Reference{}, "", ErrFileRequired
	}
	file = path.Clean(strings.TrimPrefix(file, "/"))
	if file == "." || file == ".." || strings.HasPrefix(file, "../") {
		return Reference{}, "", fmt.Errorf("%s: the file %q is outside the artifact", s, file)
	}
	return ref, file, nil
}

const itemLayout = "%s: a local OCI layout — load the image into the registry the pack is pushed to first (oci-packer copy), and name its repository: oci://<repo>:<tag>"

// ParseItem reads a pack file's oci:// item: oci://<repo>[:<tag>|@<digest>],
// a repository of the registry the pack is pushed to. The image is loaded
// there beforehand, and the registry is the pack's own — an item names no
// host, which also keeps a pack valid in any registry it is copied to; nor
// a local layout, which a registry cannot mount from.
func ParseItem(s string) (Reference, error) {
	rest, ok := strings.CutPrefix(s, OciScheme.String())
	if !ok || len(rest) == 0 {
		return Reference{}, ErrInvalid
	}
	// a name without a slash is a repository whatever it looks like: db:5432 is a tag, reg.io a name
	form := classify(rest)
	if !strings.Contains(rest, "/") && form == formRegistry {
		form = formBare
	}
	switch form {
	case formLayout:
		return Reference{}, fmt.Errorf(itemLayout, s)
	case formRegistry:
		return Reference{}, fmt.Errorf("%s: an item names no registry — the image is in the registry the pack is pushed to: oci://<repo>:<tag>", s)
	}
	if name, _, _ := strings.Cut(rest, "@"); strings.Count(name, ":") > 1 {
		return Reference{}, fmt.Errorf(itemLayout, s) // <path>:<repo>:<tag>
	}
	ref, _, err := parse(false, rest, false)
	return ref, err
}

// IsLayout reports whether an oci:// reference is to a local OCI layout
// rather than to a registry.
func IsLayout(s string) bool {
	return OciScheme.IsPrefix(s) && isLayout(strings.TrimPrefix(s, OciScheme.String()))
}

// IsRemote reports whether an oci:// reference is to a registry.
func IsRemote(s string) bool {
	return OciScheme.IsPrefix(s) && !isLayout(strings.TrimPrefix(s, OciScheme.String()))
}

// form is what the text after oci:// looks like.
type form int

const (
	formBare     form = iota // no host and no path to a directory: <repo>[:<tag>], or anything else
	formLayout               // a local layout: <path>:<repo>:<tag>
	formRegistry             // <host>[:<port>]/<repo>:<tag>
)

// classify tells a layout's <path>:<repo>:<tag> from a registry's
// <host>[:<port>]/<repo>:<tag> by its form. A path starting with /, . or ~
// is a layout's. Otherwise the first segment, up to the first /, decides: a
// colon followed by a port — a registry (localhost:5000); a colon followed
// by anything else and a slash after it — a layout (layout-dir:my/image);
// a dot, or localhost — a registry (docker.io, localho.st, 10.0.0.1);
// anything else is bare.
func classify(rest string) form {
	if strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "~") {
		return formLayout
	}
	first, _, hasSlash := strings.Cut(rest, "/")
	if _, after, ok := strings.Cut(first, ":"); ok {
		switch {
		case isPort(after):
			return formRegistry
		case hasSlash:
			return formLayout
		}
		return formBare
	}
	if strings.Contains(first, ".") || first == "localhost" {
		return formRegistry
	}
	return formBare
}

// isLayout: a reference to a registry is the registry form; all the rest is a layout's. So a relative layout
// whose first segment looks like a host is written with ./ in front.
func isLayout(rest string) bool { return classify(rest) != formRegistry }

func isPort(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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

// parse reads what follows oci://: a registry's <host>/<repo>:<tag> when
// remote, a layout's <path>:<repo>:<tag> otherwise. withFile: a path follows the
// tag or the digest after one more colon, and is returned as it is.
func parse(remote bool, imagePathWithRef string, withFile bool) (Reference, string, error) {
	var ref Reference

	if strings.Contains(imagePathWithRef, "//") {
		return ref, "", ErrInvalid
	}

	if remote {
		idx := strings.IndexByte(imagePathWithRef, '/')
		if idx == -1 {
			ref.Path = imagePathWithRef
		} else {
			ref.Host = imagePathWithRef[:idx]
			if len(ref.Host) == 0 {
				return ref, "", ErrHostnameRequired
			}
			imagePathWithRef = strings.TrimPrefix(imagePathWithRef[idx+1:], "/")
		}
	}

	ref.Scheme = OciScheme

	var file string
	idx := strings.IndexAny(imagePathWithRef, "@:")
	if idx == -1 {
		ref.Path, ref.Ref = imagePathWithRef, "latest"
	} else {
		ref.Path, ref.Ref = imagePathWithRef[:idx], imagePathWithRef[idx+1:]

		if withFile {
			// a digest is <algo>:<hex>, so its colon is not the path's
			skip := 0
			if imagePathWithRef[idx] == '@' {
				skip = strings.IndexByte(ref.Ref, ':') + 1
			}
			if next := strings.IndexByte(ref.Ref[skip:], ':'); next >= 0 {
				ref.Ref, file = ref.Ref[:skip+next], ref.Ref[skip+next+1:]
			}
		}
		if imagePathWithRef[idx] == '@' {
			if _, err := digest.Parse(ref.Ref); err != nil {
				return Reference{}, "", ErrInvalid
			}
		}
	}

	if len(ref.Path) == 0 || strings.HasSuffix(ref.Path, "/") {
		return Reference{}, "", ErrInvalid
	}

	return ref, file, nil
}

func ParseRegistryReference(ref string) (Reference, error) {
	if len(ref) > 0 && !isDigest(ref) && strings.ContainsAny(ref, "@:") {
		// [oci://registry.host/[repo/]]image[:tag|@digest]
		var err error
		var parsedReference Reference
		if found := strings.Contains(ref, "://"); found {
			if !OciScheme.IsPrefix(ref) {
				return Reference{}, ErrSchemeUnsupported
			}
			parsedReference, err = Parse(ref)
		} else {
			parsedReference, _, err = parse(true, ref, false)
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
	return OciScheme.IsPrefix(from)
}
