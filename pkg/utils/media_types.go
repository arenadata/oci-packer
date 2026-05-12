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

package utils

import (
	"mime"
	"path/filepath"
	"strings"

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
			return strings.Split(mimeType, ";")[0]
		}
	}

	return defaultMediaType
}
