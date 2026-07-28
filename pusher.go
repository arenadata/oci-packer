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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/internal/parallel"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/platforms"
	"github.com/docker/go-units"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
)

type Pusher interface {
	Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error)
}

type index struct {
	Type string

	Manifests   []manifest
	Annotations map[string]string

	budget *parallel.Budget
}

func (i index) Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	log := logger.New("index_push")
	log.Debug("building index OCI-manifest for pushing")

	index := ocispecv1.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispecv1.MediaTypeImageIndex,
		ArtifactType: i.Type,
		Annotations:  i.Annotations,
		// Sized up front: the manifests go up concurrently, but the index has to
		// list them in the order the pack file gave.
		Manifests: make([]ocispecv1.Descriptor, len(i.Manifests)),
	}

	err := i.budget.Each(ctx, len(i.Manifests), func(ctx context.Context, n int) error {
		m := i.Manifests[n]
		log.Debug("push manifest")

		desc, err := m.Push(ctx, pusher)
		if err != nil {
			log.WithError(err).Errorf("failed to push manifest")
			return err
		}

		if len(m.Platform) > 0 {
			p, err := platforms.Parse(m.Platform)
			if err != nil {
				log.WithError(err).WithField("platform", m.Platform).Errorf("failed to parse platform")
				return err
			}
			desc.Platform = &p
			log.WithField("platform", platforms.FormatAll(p)).Debug("descriptor created for platform")
		}

		index.Manifests[n] = desc

		return nil
	})
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	log.WithField("manifests_count", len(index.Manifests)).Debug("committing index manifest")
	return commit(ctx, i.budget, pusher, index, index.MediaType, index.ArtifactType)
}

type manifest struct {
	Metadata
	Type        string
	Descriptors []Descriptor
	Platform    string

	budget *parallel.Budget
}

func (m manifest) Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	log := logger.New("manifest_push")
	log.Debug("building manifest")

	manifest := ocispecv1.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispecv1.MediaTypeImageManifest,
		ArtifactType: m.Type,
		Annotations:  m.Annotations,
		// Sized up front: the layers go up concurrently, but the manifest has to
		// list them in the order the pack file gave.
		Layers: make([]ocispecv1.Descriptor, len(m.Descriptors)),
	}

	// The config and the layers are independent blobs, so they all go together.
	// Position 0 is the config, keeping the order a sequential pack used.
	const configSlot = 0

	err := m.budget.Each(ctx, len(m.Descriptors)+1, func(ctx context.Context, n int) error {
		if n == configSlot {
			desc, err := m.pushConfig(ctx, pusher)
			if err != nil {
				log.WithError(err).WithField("digest", desc.Digest).Error("failed to push config")
				return err
			}
			log.WithField("digest", desc.Digest).Debug("config pushed successfully")
			manifest.Config = desc

			return nil
		}

		d := m.Descriptors[n-1]
		handler := m.pushFile
		if reference.IsOCI(d.From) {
			handler = m.mount
		}

		desc, err := handler(ctx, pusher, d)
		if err != nil {
			return err
		}
		log.WithField("digest", desc.Digest).Debug("pushed descriptor layer")
		manifest.Layers[n-1] = desc

		return nil
	})
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	log.WithField("layers_count", len(manifest.Layers)).Debug("committing manifest")
	return commit(ctx, m.budget, pusher, manifest, manifest.MediaType, manifest.ArtifactType)
}

func (m manifest) mount(ctx context.Context, pusher registry.Pusher, d Descriptor) (ocispecv1.Descriptor, error) {
	log := logger.New("mount_repository")

	ref := strings.TrimPrefix(d.From, reference.RegistryScheme.String())
	parsedRef, err := reference.ParseRegistryReference(ref)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	var desc ocispecv1.Descriptor
	err = m.budget.Slot(ctx, func() (err error) {
		desc, err = pusher.MountFrom(ctx, parsedRef)
		return err
	})
	if err != nil {
		log.WithError(err).WithField("from", ref).Error("failed to mount repository")
	} else {
		log.WithField("from", ref).Debug("mounted repository")
	}
	return desc, err
}

// pushFile reads a local file, works out its descriptor and uploads it. Reading
// the file to digest it is as much of the cost as the upload, so both happen
// inside the same slot.
func (m manifest) pushFile(ctx context.Context, pusher registry.Pusher, d Descriptor) (ocispecv1.Descriptor, error) {
	log := logger.New("push_file")

	var desc ocispecv1.Descriptor
	err := m.budget.Slot(ctx, func() error {
		var r io.ReadCloser
		var err error

		desc, r, err = d.FileToOciDescriptor()
		if err != nil {
			log.WithError(err).Error("failed to prepare layer descriptor")
			return err
		}
		defer func() { _ = r.Close() }()
		log.WithField("digest", desc.Digest).Debug("prepared layer")

		fields := map[string]any{
			"digest":   desc.Digest,
			"size":     units.BytesSize(float64(desc.Size)),
			"filepath": strings.TrimPrefix(d.From, reference.FileSchema.String()),
		}

		log.WithFields(fields).Debug("upload file")

		now := time.Now()
		// Two items of a pack can name the same bytes — and give them different
		// titles — so the descriptors differ while the blob behind them does not.
		err = m.budget.Once(ctx, desc.Digest, func() error {
			return upload(ctx, pusher, desc, r, log.WithFields(fields))
		})

		fields["duration"] = time.Since(now).Round(time.Millisecond).String()

		if err != nil {
			log.WithError(err).WithFields(fields).Error("uploaded file failed")
			return err
		}

		log.WithFields(fields).Info("file uploaded")

		return nil
	})
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	return desc, nil
}

// upload pushes a blob, treating "the destination already has it" as the success
// it is. Swallowing that here rather than at the call site keeps it out of the
// budget's record of what went wrong — it is not a failure, and filing it as one
// would make it the error reported for something else entirely.
func upload(ctx context.Context, pusher registry.Pusher, desc ocispecv1.Descriptor, r io.Reader, log *logrus.Entry) error {
	if err := pusher.Push(ctx, desc, r); err != nil {
		if !registry.IsAlreadyExists(err) {
			return err
		}
		log.Debug("destination already has this blob")
	}

	return nil
}

func (m manifest) pushConfig(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	if m.Config == nil {
		log := logger.New("push_config")
		desc := ocispecv1.DescriptorEmptyJSON

		// Every manifest of an index synthesises the same empty config, so this
		// is the one blob a multi-platform pack is guaranteed to push twice.
		err := m.budget.Slot(ctx, func() error {
			return m.budget.Once(ctx, desc.Digest, func() error {
				return upload(ctx, pusher, desc, bytes.NewReader(desc.Data), log)
			})
		})
		if err != nil {
			return ocispecv1.Descriptor{}, err
		}
		return desc, nil
	}

	return m.pushFile(ctx, pusher, m.Config.ToDescriptor())
}

func newDescriptorWithReader(manifest any, mt string) (ocispecv1.Descriptor, io.Reader, error) {
	b, err := json.Marshal(manifest)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	return NewDescriptorFromBytes(mt, b), bytes.NewReader(b), nil
}

func commit(ctx context.Context, b *parallel.Budget, pusher registry.Pusher, v any, mt, at string) (ocispecv1.Descriptor, error) {
	desc, r, err := newDescriptorWithReader(v, mt)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	log := logger.New("commit")
	err = b.Slot(ctx, func() error {
		return b.Once(ctx, desc.Digest, func() error { return upload(ctx, pusher, desc, r, log) })
	})
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}
	desc.ArtifactType = at

	return desc, nil
}
