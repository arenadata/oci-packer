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

package remote

import (
	"context"
	"io"
	"net/http"

	"github.com/arenadata/oci-packer/pkg/registry/client"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type registryFetcher struct {
	ref    reference.Reference
	client *client.Client

	plainHttp bool
}

func (r registryFetcher) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	repoRef := r.ref
	repoRef.Ref = desc.Digest.String()

	repoUrl := fromRef(repoRef, r.plainHttp)
	req, err := newGetRequest(ctx, repoUrl.blobs())
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, remoteerror.NewUnexpectedStatusErr(resp)
	}

	return resp.Body, nil
}
