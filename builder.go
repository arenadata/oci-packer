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
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/internal/parallel"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
)

// DefaultConcurrency is how much of a pack runs at once when the caller does not
// ask for a number. It matches the copier's: both wait on the same registry.
const DefaultConcurrency = parallel.DefaultConcurrency

type BuildOption func(*builderOptions)

func WithTmpDir(tmpDir string) BuildOption {
	return func(o *builderOptions) {
		o.tmpDir = tmpDir
	}
}

// WithConcurrency sets how much of the pack runs at once: how many sources are
// pulled in while the pack is being built, and how many blobs are pushed out
// while it is being uploaded. Values below 1 are clamped to 1, which does
// everything in order, one at a time, as packing did before it ran in parallel.
func WithConcurrency(n int) BuildOption {
	return func(o *builderOptions) {
		o.concurrency = n
	}
}

type builderOptions struct {
	tmpDir      string
	concurrency int
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
	for _, item := range p.Items {
		if reference.IsOCI(item.From) && item.Config != nil {
			return ocispecv1.Descriptor{}, fmt.Errorf("item %s: a mounted reference is the member itself and cannot carry a config", item.From)
		}
	}

	options := builderOptions{concurrency: DefaultConcurrency}
	for _, opt := range opts {
		opt(&options)
	}

	if len(options.tmpDir) == 0 {
		options.tmpDir = os.TempDir()
	}

	// One budget for the whole pack, so that the sources coming in and the blobs
	// going out share a single ceiling instead of each getting their own.
	budget := parallel.NewBudget(options.concurrency)

	log.WithFields(map[string]any{
		"index_expected": indexExpected,
		"concurrency":    budget.Limit(),
	}).Debug("pack configuration validated")

	build := p.makeManifest
	if indexExpected {
		build = p.makeIndex
	}

	pusher, err := build(ctx, options, budget)
	if err != nil {
		log.WithError(err).Error("failed to build pack")
		return ocispecv1.Descriptor{}, budget.Cause(err)
	}

	desc, err := pusher.Push(ctx, resolver)
	if err != nil {
		return ocispecv1.Descriptor{}, budget.Cause(err)
	}

	return desc, nil
}

func (p Pack) makeIndex(ctx context.Context, opts builderOptions, b *parallel.Budget) (Pusher, error) {
	log := logger.New("make_index")
	log.Debug("creating index object")

	indexObject := &index{
		Type:        p.Type,
		Annotations: extendAnnotations(p.Metadata.Annotations),
		Manifests:   make([]manifest, len(p.Items)),
		budget:      b,
	}

	err := p.eachItem(ctx, opts, b, log, func(n int, item Descriptor, descriptors []Descriptor) {
		typ := item.Type
		if len(typ) == 0 && !reference.IsOCI(item.From) {
			typ = indexObject.Type // a mounted member keeps its own artifactType
		}
		indexObject.Manifests[n] = manifest{
			Metadata:    Metadata{Config: item.Config},
			Type:        typ,
			Descriptors: descriptors,
			Platform:    item.Platform,
			budget:      b,
		}
	})
	if err != nil {
		return nil, err
	}

	log.WithField("manifests_count", len(indexObject.Manifests)).Debug("index object created successfully")
	return indexObject, nil
}

func (p Pack) makeManifest(ctx context.Context, opts builderOptions, b *parallel.Budget) (Pusher, error) {
	log := logger.New("make_manifest")
	log.Debug("creating manifest object")

	manifestObject := &manifest{Metadata: p.Metadata, Type: p.Type, budget: b}
	manifestObject.Annotations = extendAnnotations(manifestObject.Annotations)

	// One item can expand to many descriptors (a dir:// walk), so collect them
	// per item and flatten afterwards rather than appending as they arrive: the
	// layers must end up in pack-file order however the sources came in.
	perItem := make([][]Descriptor, len(p.Items))
	err := p.eachItem(ctx, opts, b, log, func(n int, _ Descriptor, descriptors []Descriptor) {
		perItem[n] = descriptors
	})
	if err != nil {
		return nil, err
	}
	for _, descriptors := range perItem {
		manifestObject.Descriptors = append(manifestObject.Descriptors, descriptors...)
	}

	log.WithField("layers_count", len(manifestObject.Descriptors)).
		Debug("manifest object created successfully")

	return manifestObject, nil
}

// eachItem resolves every item of the pack — downloading an http:// source,
// walking a dir:// one — and hands each result to collect along with the
// position it came from. Items are resolved concurrently, so collect is called
// from several goroutines and must only touch its own slot.
func (p Pack) eachItem(
	ctx context.Context,
	opts builderOptions,
	b *parallel.Budget,
	log *logrus.Entry,
	collect func(n int, item Descriptor, descriptors []Descriptor),
) error {
	return b.Each(ctx, len(p.Items), func(ctx context.Context, n int) error {
		item := p.Items[n]

		fields := map[string]any{"from": item.From}
		if len(item.Platform) > 0 {
			fields["platform"] = item.Platform
		}
		log.WithFields(fields).Debug("processing item")

		descriptors, err := handleItem(ctx, item, opts, b)
		if err != nil {
			log.WithError(err).WithFields(fields).Error("failed to process item")
			return err
		}

		collect(n, item, descriptors)

		return nil
	})
}

func handleItem(ctx context.Context, item Descriptor, opts builderOptions, b *parallel.Budget) ([]Descriptor, error) {
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
	} else if reference.RegistryScheme.IsPrefix(item.From) {
		handlerType = "OCI"
		handler = ociHandler(item)
	} else {
		return nil, errors.New("unsupported source type: " + item.From)
	}

	fields := map[string]any{"handler": handlerType, "source": item.From}
	log.WithFields(fields).Debug("selected handler for item")

	// Resolving a source is leaf work — an http:// download, a directory walk —
	// so it takes a slot for its duration and waits on nothing while holding it.
	var result []Descriptor
	err := b.Slot(ctx, func() (err error) {
		result, err = handler(ctx)
		return err
	})
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
