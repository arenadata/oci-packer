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
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Pack struct {
	Metadata `json:",inline" yaml:",inline"`

	Type  string       `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Items []Descriptor `json:"items" yaml:"items" jsonschema:"minItems=1"`
}

type Metadata struct {
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type Descriptor struct {
	From   string            `yaml:"from" json:"from"`
	Type   string            `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type ConfigDescriptor struct {
	From string `yaml:"from" json:"from"`
	Type string `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

func (d Descriptor) ToOciDescriptor() (ocispec.Descriptor, io.ReadCloser, error) {
	var desc ocispec.Descriptor
	var reader io.ReadCloser

	if !IsFile(d.From) {
		return ocispec.Descriptor{}, nil, fmt.Errorf("%s is not a file", d.From)
	}

	from := strings.TrimPrefix(d.From, FileSchema)
	fi, err := os.Open(from)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	desc, err = NewDescriptorFromFileDescriptor(fi)
	if err != nil {
		_ = fi.Close()
		return ocispec.Descriptor{}, nil, err
	}

	reader = fi

	desc.Annotations = d.Annotations
	desc.ArtifactType = d.Type

	return desc, reader, nil
}

func (p Pack) Validate() error {
	return nil
}

func (d ConfigDescriptor) ToDescriptor() Descriptor {
	return Descriptor{
		From:        d.From,
		Type:        d.Type,
		Platform:    d.Platform,
		Annotations: d.Annotations,
	}
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
