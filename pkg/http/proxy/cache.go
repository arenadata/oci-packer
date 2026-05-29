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

package proxy

import (
	"strings"
	"sync"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type Cache interface {
	Set(repoUrl, platform, title string, desc ocispecv1.Descriptor)
	Get(repoUrl, platform, title string) ocispecv1.Descriptor
	Del(repoUrl, platform, title string)
}

type memory struct {
	mu sync.RWMutex

	data map[string]map[string]ocispecv1.Descriptor
}

func newMemoryCache() Cache {
	return &memory{data: make(map[string]map[string]ocispecv1.Descriptor)}
}

func (m *memory) Set(repoUrl, platform, title string, dgst ocispecv1.Descriptor) {
	key := strings.Join([]string{platform, title}, "|")

	m.mu.Lock()

	if _, ok := m.data[repoUrl]; !ok {
		m.data[repoUrl] = make(map[string]ocispecv1.Descriptor)
	}
	m.data[repoUrl][key] = dgst

	m.mu.Unlock()
}

func (m *memory) Get(repoUrl, platform, title string) ocispecv1.Descriptor {
	key := strings.Join([]string{platform, title}, "|")

	m.mu.RLock()
	dgst := m.data[repoUrl][key]
	m.mu.RUnlock()

	return dgst
}

func (m *memory) Del(repoUrl, platform, title string) {
	key := strings.Join([]string{platform, title}, "|")

	m.mu.Lock()
	repo := m.data[repoUrl]
	delete(repo, key)
	m.data[repoUrl] = repo
	m.mu.Unlock()
}
