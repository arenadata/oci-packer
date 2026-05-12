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
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry/client"
	"github.com/arenadata/oci-packer/pkg/utils"

	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	OciScheme    = "oci://"
	DirSchema    = "dir://"
	FileSchema   = "file://"
	S3Schema     = "s3://"
	S3httpSchema = "s3+http://"
)

type ConvertHandler func(context.Context) ([]Descriptor, error)

func FileHandler(desc Descriptor) ConvertHandler {
	return func(context.Context) (descriptors []Descriptor, err error) {
		from := strings.TrimPrefix(desc.From, FileSchema)
		st, err := os.Stat(from)
		if err != nil {
			return nil, fmt.Errorf("'%s' does not exist", desc.From)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("'%s' is a directory", desc.From)
		}

		t := desc.Type
		if len(t) == 0 {
			t = utils.ResolveFileMediaType(from)
		}
		descriptors = append(descriptors, Descriptor{
			Annotations: map[string]string{ocispec.AnnotationTitle: path.Base(from)},
			Type:        t,
			From:        desc.From,
			Platform:    desc.Platform,
		})

		return
	}
}

func WalkDirHandler(desc Descriptor) ConvertHandler {
	return func(context.Context) (descriptors []Descriptor, err error) {
		from := strings.TrimPrefix(desc.From, DirSchema)
		if from[len(from)-1] != '/' {
			from += "/"
		}

		st, err := os.Stat(from)
		if err != nil {
			return nil, fmt.Errorf("'%s' does not exist", desc.From)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("'%s' is not a directory", desc.From)
		}

		err = filepath.Walk(from, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				return nil
			}

			descriptors = append(descriptors, Descriptor{
				Annotations: map[string]string{ocispec.AnnotationTitle: strings.TrimPrefix(path, from)},
				From:        FileSchema + path,
				Platform:    desc.Platform,
			})

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("'%s' list failed: %v", desc.From, err)
		}

		return descriptors, nil
	}
}

func HttpHandler(desc Descriptor, tmpDir string) ConvertHandler {
	return func(ctx context.Context) (descriptors []Descriptor, err error) {
		req, err := client.NewRequest(ctx, http.MethodGet, desc.From, nil)
		if err != nil {
			return nil, err
		}

		resp, err := client.New().Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, remoteerror.NewUnexpectedStatusErr(resp)
		}

		filename := path.Base(req.URL.Path)
		filePath := filepath.Join(tmpDir, filename)

		if err = utils.ResponseToFile(resp, filePath); err != nil {
			return nil, err
		}

		desc.From = FileSchema + filePath
		descriptors = append(descriptors, desc)
		return
	}
}

func IsHTTP(from string) bool {
	return strings.HasPrefix(from, "https://") || strings.HasPrefix(from, "http://")
}

func IsS3(from string) bool {
	return strings.HasPrefix(from, S3Schema) || strings.HasPrefix(from, S3httpSchema)
}

func IsFile(from string) bool {
	return strings.HasPrefix(from, FileSchema)
}

func IsDir(from string) bool {
	return strings.HasPrefix(from, DirSchema)
}

func IsOCI(from string) bool {
	return strings.HasPrefix(from, OciScheme)
}
