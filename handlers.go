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

	"github.com/arenadata/oci-packer/internal/logger"
	packerhttp "github.com/arenadata/oci-packer/pkg/http"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type ConvertHandler func(context.Context) ([]Descriptor, error)

func fileHandler(desc Descriptor) ConvertHandler {
	return func(ctx context.Context) (descriptors []Descriptor, err error) {
		log := logger.New("file_handler")

		from := strings.TrimPrefix(desc.From, reference.FileSchema.String())
		log.WithField("file", from).Debug("processing file")

		st, err := os.Stat(from)
		if err != nil {
			log.WithError(err).WithField("file", from).Error("file does not exist")
			return nil, fmt.Errorf("'%s' does not exist", desc.From)
		}
		if st.IsDir() {
			log.WithField("file", from).Error("path is a directory")
			return nil, fmt.Errorf("'%s' is a directory", desc.From)
		}

		t := desc.Type
		if len(t) == 0 {
			t = ResolveFileMediaType(from)
		}

		log.WithFields(map[string]any{
			"file": from,
			"type": t,
			"size": st.Size(),
		}).Debug("file processed successfully")

		annotations := desc.Annotations
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[ocispecv1.AnnotationTitle] = path.Base(from)

		descriptors = append(descriptors, Descriptor{
			Annotations: annotations,
			Type:        t,
			From:        desc.From,
			Platform:    desc.Platform,
		})

		return
	}
}

func walkDirHandler(desc Descriptor) ConvertHandler {
	return func(ctx context.Context) (descriptors []Descriptor, err error) {
		log := logger.New("dir_handler")

		from := strings.TrimPrefix(desc.From, reference.DirSchema.String())
		log.WithField("directory", from).Debug("processing directory")

		st, err := os.Stat(from)
		if err != nil {
			log.WithError(err).WithField("directory", from).Error("directory does not exist")
			return nil, fmt.Errorf("'%s' does not exist", desc.From)
		}
		if !st.IsDir() {
			log.WithField("directory", from).Error("path is not a directory")
			return nil, fmt.Errorf("'%s' is not a directory", desc.From)
		}
		if from[len(from)-1] != '/' {
			from += "/"
		}

		err = filepath.Walk(from, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				return nil
			}

			descriptors = append(descriptors, Descriptor{
				Annotations: map[string]string{
					ocispecv1.AnnotationTitle: strings.TrimPrefix(path, from),
					// The directory the item named, so that what was packed from
					// `dir://templates/` can be put back under templates/ — the title
					// alone is relative to that directory and does not remember it.
					AnnotationDir: strings.TrimSuffix(strings.TrimPrefix(desc.From, reference.DirSchema.String()), "/"),
				},
				From:     reference.FileSchema.String() + path,
				Platform: desc.Platform,
			})

			return nil
		})
		if err != nil {
			log.WithError(err).WithField("directory", from).Error("failed to walk directory")
			return nil, fmt.Errorf("'%s' list failed: %v", desc.From, err)
		}

		log.WithFields(map[string]any{
			"directory": from,
			"files":     len(descriptors),
		}).Debug("directory processed successfully")

		return descriptors, nil
	}
}

func httpHandler(desc Descriptor, tmpDir string) ConvertHandler {
	return func(ctx context.Context) (descriptors []Descriptor, err error) {
		log := logger.New("http_handler")
		log.WithField("url", desc.From).Debug("downloading from http")

		req, err := packerhttp.NewRequest(ctx, http.MethodGet, desc.From, nil)
		if err != nil {
			log.WithError(err).WithField("url", desc.From).Error("failed to create http request")
			return nil, err
		}

		resp, err := packerhttp.New().Do(req)
		if err != nil {
			log.WithError(err).WithField("url", desc.From).Error("http request failed")
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			log.WithField("status_code", resp.StatusCode).Error("http request returned non-OK status")
			return nil, remoteerror.NewUnexpectedStatusErr(resp)
		}

		// Each source downloads into a directory of its own, named after the URL.
		// Two items can share a basename — .../linux/app.tar.gz and
		// .../darwin/app.tar.gz — and writing both to tmpDir/app.tar.gz gives one
		// item the other's bytes. The name is derived from the URL rather than
		// picked at random so ResponseToFile can still skip a file already
		// downloaded by an earlier run.
		filePath := filepath.Join(tmpDir, sourceKey(desc.From), path.Base(req.URL.Path))

		log.WithFields(map[string]any{
			"url":  desc.From,
			"file": filePath,
		}).Debug("saving http response to file")

		if err = packerhttp.ResponseToFile(resp, filePath); err != nil {
			log.WithError(err).WithField("file", filePath).Error("failed to save http response to file")
			return nil, err
		}

		log.WithFields(map[string]any{
			"url":  desc.From,
			"file": filePath,
		}).Debug("download completed successfully")

		desc.From = reference.FileSchema.String() + filePath
		descriptors = append(descriptors, desc)
		return
	}
}

// sourceKey is a short, stable directory name for a download source, unique per
// URL so that two sources cannot write over each other.
func sourceKey(url string) string {
	return digest.FromString(url).Encoded()[:12]
}

func ociHandler(desc Descriptor) ConvertHandler {
	return func(context.Context) (descriptors []Descriptor, err error) {
		descriptors = append(descriptors, desc)
		return
	}
}
