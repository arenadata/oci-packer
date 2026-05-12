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

package packer

import (
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const defaultMediaType = "application/octet-stream"

func ResolveFileMediaType(filename string) string {
	ext := filepath.Ext(filename)
	if len(ext) > 0 {
		switch ext {
		case ".tar":
			return ocispec.MediaTypeImageLayer
		case ".tar.gz", ".tgz":
			return ocispec.MediaTypeImageLayerGzip
		case ".tar.zst":
			return ocispec.MediaTypeImageLayerZstd
		}

		mimeType := mime.TypeByExtension(ext)
		if len(mimeType) > 0 {
			return strings.SplitN(mimeType, ";", 2)[0]
		}
	}

	return defaultMediaType
}

func NewDescriptorFromFileDescriptor(file *os.File) (ocispec.Descriptor, error) {
	dig, err := digest.FromReader(file)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if _, err = file.Seek(0, 0); err != nil {
		return ocispec.Descriptor{}, err
	}
	st, err := file.Stat()
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	return ocispec.Descriptor{
		MediaType: ResolveFileMediaType(file.Name()),
		Digest:    dig,
		Size:      st.Size(),
	}, nil
}

func NewDescriptorFromBytes(mediaType string, content []byte) ocispec.Descriptor {
	if len(mediaType) == 0 {
		mediaType = defaultMediaType
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
}
