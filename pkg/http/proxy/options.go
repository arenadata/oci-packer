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

type Option func(*Proxy)

func WithReferenceConverter(conv RequestToReferenceConverter) Option {
	return func(p *Proxy) {
		p.conv = conv
	}
}

func WithCache(cache Cache) Option {
	return func(p *Proxy) {
		p.cache = cache
	}
}

func WithDescriptorFetcher(fd FetchDescriptors) Option {
	return func(p *Proxy) {
		p.fd = fd
	}
}

func WithRemotePlainHttp() Option {
	return func(p *Proxy) {
		p.plainHttp = true
	}
}

func WithRemoteInsecure() Option {
	return func(p *Proxy) {
		p.insecure = true
	}
}

func WithLayoutUnpack() Option {
	return func(p *Proxy) {
		p.unpack = true
	}
}

func WithCreds(login, password string) Option {
	return func(p *Proxy) {
		p.login = login
		p.password = password
	}
}
