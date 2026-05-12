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
	"os"
	"time"

	"github.com/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"
)

type BuildOption func(*builderOptions)

func WithTmpDir(tmpDir string) BuildOption {
	return func(o *builderOptions) {
		o.tmpDir = tmpDir
	}
}

type builderOptions struct {
	tmpDir string
}

func (p Pack) Build(ctx context.Context, opts ...BuildOption) (Pusher, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var indexExpected bool
	for _, item := range p.Items {
		if IsOCI(item.From) || len(item.Platform) > 0 {
			indexExpected = true
		}
	}

	if indexExpected && p.Config != nil {
		log.L.Warning("skipped: cannot use .metadata.config with oci:// and/or platform parameters set. Use items[*].platform instead")
	}

	var options builderOptions
	for _, opt := range opts {
		opt(&options)
	}

	if len(options.tmpDir) == 0 {
		options.tmpDir = os.TempDir()
	}

	if indexExpected {
		return makeIndex(ctx, &p, &options)
	}

	return makeManifest(ctx, &p, &options)
}

func makeIndex(ctx context.Context, p *Pack, opts *builderOptions) (Pusher, error) {
	indexObject := &index{
		Type:        p.Type,
		Annotations: extendAnnotations(p.Metadata.Annotations),
	}

	for _, item := range p.Items {
		handler := handlerFromItem(item, opts.tmpDir)
		descriptors, err := handler(ctx)
		if err != nil {
			return nil, err
		}

		typ := item.Type
		if len(typ) == 0 {
			typ = indexObject.Type
		}
		indexObject.Manifests = append(indexObject.Manifests, manifest{
			Metadata: Metadata{
				Annotations: extendAnnotations(item.Annotations),
				Config:      item.Config,
			},
			Type:        typ,
			Descriptors: descriptors,
			Platform:    item.Platform,
		})
	}

	return indexObject, nil
}

func makeManifest(ctx context.Context, p *Pack, opts *builderOptions) (Pusher, error) {
	manifestObject := &manifest{Metadata: p.Metadata, Type: p.Type}
	p.Metadata.Annotations = extendAnnotations(p.Metadata.Annotations)

	for _, item := range p.Items {
		handler := handlerFromItem(item, opts.tmpDir)
		descriptors, err := handler(ctx)
		if err != nil {
			return nil, err
		}

		manifestObject.Descriptors = append(manifestObject.Descriptors, descriptors...)
	}

	return manifestObject, nil
}

func handlerFromItem(item Descriptor, tmpDir string) ConvertHandler {
	if IsHTTP(item.From) {
		return HttpHandler(item, tmpDir)
	} else if IsFile(item.From) {
		return FileHandler(item)
	} else if IsDir(item.From) {
		return WalkDirHandler(item)
	}

	return nil
}

func LoadFromFile(path string) (*Pack, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var uploader Pack
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err = dec.Decode(&uploader); err != nil {
		return nil, err
	}

	return &uploader, nil
}

func extendAnnotations(annotations map[string]string) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[ocispec.AnnotationCreated] = time.Now().UTC().Format(time.RFC3339)

	return annotations
}
