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

package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"strings"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

// ErrNoFile is returned when the artifact packs no such file.
var ErrNoFile = errors.New("no such file in the artifact")

// File is a packed file of an artifact: where it sits in the artifact's tree
// and the layer that holds it.
type File struct {
	Path string
	Desc ocispecv1.Descriptor
}

// List returns the files the artifact ref names packs itself — a manifest's
// layers, or, for an index, the layers of the members that were built from
// files (what Extract writes into the artifact's own directory). A mounted image
// or a mounted artifact is not a file of this one; o.NameBy tells an artifact
// that names itself. The files come in the order of their paths.
func List(ctx context.Context, src registry.Resolver, ref reference.Reference, o Options) ([]File, error) {
	desc, err := src.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	e := &extractor{src: src, o: o.withDefaults()}
	st, err := e.read(ctx, desc)
	if err != nil {
		return nil, err
	}

	var out []File
	if st.Manifest != nil {
		out, err = layers(*st.Manifest)
		if err != nil {
			return nil, err
		}
	} else {
		parts, err := e.parts(ctx, st.Index)
		if err != nil {
			return nil, err
		}

		for _, p := range parts {
			if p.Kind != kindFiles {
				continue
			}

			fs, err := layers(p.Manifest)
			if err != nil {
				return nil, err
			}
			out = append(out, fs...)
		}
	}

	return out, nil
}

func layers(man ocispecv1.Manifest) ([]File, error) {
	out := make([]File, 0, len(man.Layers))
	for _, l := range man.Layers {
		p, err := layerPath(l)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, Desc: l})
	}
	return out, nil
}

// Open returns one packed file of the artifact ref names, by its path in the
// artifact's tree (templates/app.tmpl): only that layer is fetched. The caller
// closes the reader.
func Open(ctx context.Context, src registry.Resolver, ref reference.Reference, file string, o Options) (ocispecv1.Descriptor, io.ReadCloser, error) {
	want := path.Clean(strings.TrimPrefix(file, "/"))
	files, err := List(ctx, src, ref, o)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}
	for _, f := range files {
		if f.Path == want {
			r, err := src.Fetch(ctx, reference.Reference{Ref: f.Desc.Digest.String()}.WithDescriptor(f.Desc))
			if err != nil {
				return ocispecv1.Descriptor{}, nil, err
			}
			return f.Desc, r, nil
		}
	}
	have := make([]string, len(files))
	for i, f := range files {
		have[i] = f.Path
	}
	return ocispecv1.Descriptor{}, nil, fmt.Errorf("%w: %s (it packs %s)", ErrNoFile, want, strings.Join(have, ", "))
}

// Descriptors are the layers of the files List finds, each with its title set to the file's path in the artifact's
// tree (a dir:// item's directory in front): the shape of proxy.FetchDescriptors, so that
// proxy.WithDescriptorFetcher(extract.Options{}.Descriptors) serves a file by that path. opts are not used.
func (o Options) Descriptors(ctx context.Context, src registry.Resolver, ref reference.Reference, _ map[string]string) ([]ocispecv1.Descriptor, error) {
	files, err := List(ctx, src, ref, o)
	if err != nil {
		return nil, err
	}
	out := make([]ocispecv1.Descriptor, len(files))
	for i, f := range files {
		d := f.Desc
		d.Annotations = map[string]string{}
		maps.Copy(d.Annotations, f.Desc.Annotations)
		d.Annotations[ocispecv1.AnnotationTitle] = f.Path
		out[i] = d
	}
	return out, nil
}
