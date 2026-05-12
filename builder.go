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
	"errors"
	"os"
	"time"

	"github.com/arenadata/oci-packer/pkg/registry"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type buildOption func(*builderOptions)

func WithTmpDir(tmpDir string) buildOption {
	return func(o *builderOptions) {
		o.tmpDir = tmpDir
	}
}

type builderOptions struct {
	tmpDir string
}

func (p Pack) Pack(ctx context.Context, resolver registry.Pusher, opts ...buildOption) (ocispec.Descriptor, error) {
	if err := p.Validate(); err != nil {
		return ocispec.Descriptor{}, err
	}

	var indexExpected bool
	for _, item := range p.Items {
		if IsOCI(item.From) || len(item.Platform) > 0 {
			indexExpected = true
		}
	}

	if indexExpected && p.Config != nil {
		msg := "cannot use .metadata.config with oci:// and/or platform items set. Use items[*].config instead"
		return ocispec.Descriptor{}, errors.New(msg)
	}

	var options builderOptions
	for _, opt := range opts {
		opt(&options)
	}

	if len(options.tmpDir) == 0 {
		options.tmpDir = os.TempDir()
	}

	if indexExpected {
		pusher, err := p.makeIndex(ctx, options)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		return pusher.Push(ctx, resolver)
	}

	pusher, err := p.makeManifest(ctx, options)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return pusher.Push(ctx, resolver)
}

func (p Pack) makeIndex(ctx context.Context, opts builderOptions) (Pusher, error) {
	indexObject := &index{
		Type:        p.Type,
		Annotations: extendAnnotations(p.Metadata.Annotations),
	}

	for _, item := range p.Items {
		descriptors, err := handleItem(ctx, item, opts)
		if err != nil {
			return nil, err
		}

		typ := item.Type
		if len(typ) == 0 {
			typ = indexObject.Type
		}
		indexObject.Manifests = append(indexObject.Manifests, manifest{
			Metadata:    Metadata{Config: item.Config},
			Type:        typ,
			Descriptors: descriptors,
			Platform:    item.Platform,
		})
	}

	return indexObject, nil
}

func (p Pack) makeManifest(ctx context.Context, opts builderOptions) (Pusher, error) {
	manifestObject := &manifest{Metadata: p.Metadata, Type: p.Type}
	p.Metadata.Annotations = extendAnnotations(p.Metadata.Annotations)

	for _, item := range p.Items {
		descriptors, err := handleItem(ctx, item, opts)
		if err != nil {
			return nil, err
		}

		manifestObject.Descriptors = append(manifestObject.Descriptors, descriptors...)
	}

	return manifestObject, nil
}

func handleItem(ctx context.Context, item Descriptor, opts builderOptions) ([]Descriptor, error) {
	var handler ConvertHandler
	if IsHTTP(item.From) {
		handler = httpHandler(item, opts.tmpDir)
	} else if IsFile(item.From) {
		handler = fileHandler(item)
	} else if IsDir(item.From) {
		handler = walkDirHandler(item)
	}

	return handler(ctx)
}

func extendAnnotations(annotations map[string]string) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[ocispec.AnnotationCreated] = time.Now().UTC().Format(time.RFC3339)

	return annotations
}
