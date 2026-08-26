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

// Package types is the pack file format and nothing else: the types a tool
// needs to WRITE or READ a pack file without linking oci-packer itself. It
// is its own module with no dependencies, so that depending on the format
// costs nothing.
package types

// AnnotationDir marks a layer packed from a dir:// item with the directory
// the item named, as written in the pack file. extract puts the file back
// under it.
const AnnotationDir = "io.arenadata.oci-packer.dir"

// Pack is a pack file: the artifact's type and annotations, and the items
// that become its members (an index) or its layers (a manifest).
type Pack struct {
	Metadata `json:",inline" yaml:",inline"`

	Type  string       `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Items []Descriptor `json:"items" yaml:"items" jsonschema:"minItems=1"`
}

// Metadata is what the artifact carries on its root: a config and annotations.
type Metadata struct {
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// Descriptor is one item: where it comes from (file://, dir://, cr://), its
// artifact type, its platform (an image), its annotations.
type Descriptor struct {
	From   string            `yaml:"from" json:"from"`
	Type   string            `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// ConfigDescriptor is the config of the artifact or of an item.
type ConfigDescriptor struct {
	From string `yaml:"from" json:"from"`
	Type string `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

func (d ConfigDescriptor) ToDescriptor() Descriptor {
	return Descriptor{
		From:        d.From,
		Type:        d.Type,
		Platform:    d.Platform,
		Annotations: d.Annotations,
	}
}
