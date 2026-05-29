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

package reference

import (
	"fmt"
	"strings"
)

type Schema string

const (
	OciScheme      Schema = "oci"
	RegistryScheme Schema = "cr"
	DirSchema      Schema = "dir"
	FileSchema     Schema = "file"
	S3Schema       Schema = "s3"
	S3httpSchema   Schema = "s3+http"
	HttpSchema     Schema = "http"
	HttpsSchema    Schema = "https"
)

var isRemoteScheme = map[Schema]bool{
	OciScheme:      false,
	DirSchema:      false,
	FileSchema:     false,
	RegistryScheme: true,
	S3Schema:       true,
	S3httpSchema:   true,
	HttpSchema:     true,
	HttpsSchema:    true,
}

func (s Schema) String() string { return fmt.Sprintf("%s://", string(s)) }

func (s Schema) IsPrefix(v string) bool {
	return strings.HasPrefix(v, s.String())
}

func (s Schema) Eq(v string) bool {
	return string(s) == v
}

func GetScheme(s string) (Schema, error) {
	scheme, _, err := splitScheme(s)
	if err != nil {
		return "", err
	}

	return Schema(scheme), nil
}

func IsRegistryScheme(s Schema) bool {
	return OciScheme == s || RegistryScheme == s
}
