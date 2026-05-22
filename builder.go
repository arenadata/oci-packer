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
	"maps"
	"os"
	"time"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
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

func (p Pack) Pack(ctx context.Context, resolver registry.Pusher, opts ...BuildOption) (ocispecv1.Descriptor, error) {
	log := logger.New("builder")
	log.Debug("configured pack")

	if err := p.Validate(); err != nil {
		log.WithError(err).Error("pack validation failed")
		return ocispecv1.Descriptor{}, err
	}

	var indexExpected bool
	for _, item := range p.Items {
		if reference.IsOCI(item.From) || len(item.Platform) > 0 {
			indexExpected = true
		}
	}

	if indexExpected && p.Config != nil {
		log.WithField("index_expected", indexExpected).Error("configuration conflict detected")
		msg := "cannot use .metadata.config with oci://|docker:// and/or platform items set. Use items[*].config instead"
		return ocispecv1.Descriptor{}, errors.New(msg)
	}

	var options builderOptions
	for _, opt := range opts {
		opt(&options)
	}

	if len(options.tmpDir) == 0 {
		options.tmpDir = os.TempDir()
	}

	log.WithField("index_expected", indexExpected).Debug("pack configuration validated")

	if indexExpected {
		pusher, err := p.makeIndex(ctx, options)
		if err != nil {
			log.WithError(err).Error("failed to build index manifest")
			return ocispecv1.Descriptor{}, err
		}
		return pusher.Push(ctx, resolver)
	}

	pusher, err := p.makeManifest(ctx, options)
	if err != nil {
		log.WithError(err).Error("failed to build manifest image")
		return ocispecv1.Descriptor{}, err
	}
	return pusher.Push(ctx, resolver)
}

func (p Pack) makeIndex(ctx context.Context, opts builderOptions) (Pusher, error) {
	log := logger.New("make_index")
	log.Debug("creating index object")

	indexObject := &index{
		Type:        p.Type,
		Annotations: extendAnnotations(p.Metadata.Annotations),
	}

	for _, item := range p.Items {
		fields := map[string]any{"from": item.From}
		if len(item.Platform) > 0 {
			fields["platform"] = item.Platform
		}
		log.WithFields(fields).Debug("processing item")

		descriptors, err := handleItem(ctx, item, opts)
		if err != nil {
			log.WithError(err).WithFields(fields).Error("failed to process index item")
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

	log.WithField("manifests_count", len(indexObject.Manifests)).Debug("index object created successfully")
	return indexObject, nil
}

func (p Pack) makeManifest(ctx context.Context, opts builderOptions) (Pusher, error) {
	log := logger.New("make_manifest")
	log.Debug("creating manifest object")

	manifestObject := &manifest{Metadata: p.Metadata, Type: p.Type}
	manifestObject.Annotations = extendAnnotations(manifestObject.Annotations)

	for _, item := range p.Items {
		fields := map[string]any{"from": item.From}
		if len(item.Platform) > 0 {
			fields["platform"] = item.Platform
		}
		log.WithFields(fields).Debug("processing item")

		descriptors, err := handleItem(ctx, item, opts)
		if err != nil {
			log.WithError(err).WithFields(fields).Error("failed to process index item")
			return nil, err
		}

		manifestObject.Descriptors = append(manifestObject.Descriptors, descriptors...)
	}

	log.WithField("layers_count", len(manifestObject.Descriptors)).
		Debug("manifest object created successfully")

	return manifestObject, nil
}

func handleItem(ctx context.Context, item Descriptor, opts builderOptions) ([]Descriptor, error) {
	log := logger.New("handle_item")

	var handler ConvertHandler
	handlerType := "unknown"

	if reference.IsHTTP(item.From) {
		handlerType = "HTTP"
		handler = httpHandler(item, opts.tmpDir)
	} else if reference.IsFile(item.From) {
		handlerType = "File"
		handler = fileHandler(item)
	} else if reference.IsDir(item.From) {
		handlerType = "Directory"
		handler = walkDirHandler(item)
	} else {
		return nil, errors.New("unsupported source type: " + item.From)
	}

	fields := map[string]any{"handler": handlerType, "source": item.From}
	log.WithFields(fields).Debug("selected handler for item")

	result, err := handler(ctx)
	if err != nil {
		log.WithError(err).WithFields(fields).Error("handler execution failed")
		return nil, err
	}

	fields["descriptors_count"] = len(result)
	log.WithFields(fields).Debug("handler execution succeeded")

	return result, nil
}

func extendAnnotations(annotations map[string]string) map[string]string {
	newAnnotations := make(map[string]string)
	maps.Copy(newAnnotations, annotations)
	newAnnotations[ocispecv1.AnnotationCreated] = time.Now().UTC().Format(time.RFC3339)
	return newAnnotations
}
