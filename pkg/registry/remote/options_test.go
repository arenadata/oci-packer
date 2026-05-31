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
	"testing"

	packerhttp "github.com/arenadata/oci-packer/pkg/http"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithPlainHttp_Option(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithPlainHttp()
	option(client)

	assert.True(t, client.plainHttp)
}

func TestWithPlainHttp_DefaultIsFalse(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	assert.False(t, client.plainHttp)
}

func TestWithInsecure_Option(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithInsecure()
	option(client)

	assert.True(t, client.insecure)
}

func TestWithInsecure_DefaultIsFalse(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	assert.False(t, client.insecure)
}

func TestWithCreds_Option(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithCreds("testuser", "testpass")
	option(client)

	assert.Equal(t, "testuser", client.login)
	assert.Equal(t, "testpass", client.password)
}

func TestWithCreds_EmptyCredentials(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithCreds("", "")
	option(client)

	assert.Equal(t, "", client.login)
	assert.Equal(t, "", client.password)
}

func TestWithCreds_SpecialCharacters(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithCreds("user@domain.com", "p@$$w0rd!")
	option(client)

	assert.Equal(t, "user@domain.com", client.login)
	assert.Equal(t, "p@$$w0rd!", client.password)
}

func TestWithClient_Option(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	customClient := packerhttp.New()
	option := WithClient(customClient)
	option(client)

	assert.Equal(t, customClient, client.client)
}

func TestWithClient_NilClient(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	option := WithClient(nil)
	option(client)

	assert.Nil(t, client.client)
}

func TestOptions_MultipleApplications(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	opts := []Option{
		WithPlainHttp(),
		WithInsecure(),
		WithCreds("user", "pass"),
	}

	for _, opt := range opts {
		opt(client)
	}

	assert.True(t, client.plainHttp)
	assert.True(t, client.insecure)
	assert.Equal(t, "user", client.login)
	assert.Equal(t, "pass", client.password)
}

func TestOptions_OverrideCreds(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	// Apply first set of credentials
	opt1 := WithCreds("user1", "pass1")
	opt1(client)

	assert.Equal(t, "user1", client.login)
	assert.Equal(t, "pass1", client.password)

	// Override with second set
	opt2 := WithCreds("user2", "pass2")
	opt2(client)

	assert.Equal(t, "user2", client.login)
	assert.Equal(t, "pass2", client.password)
}

func TestOptions_OverrideClient(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{ref: parsedRef}

	client1 := packerhttp.New()
	opt1 := WithClient(client1)
	opt1(client)

	assert.Equal(t, client1, client.client)

	client2 := packerhttp.New()
	opt2 := WithClient(client2)
	opt2(client)

	assert.Equal(t, client2, client.client)
}

func TestWithPlainHttp_DoesNotAffectOtherFields(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{
		ref:      parsedRef,
		insecure: true,
		login:    "user",
		password: "pass",
	}

	option := WithPlainHttp()
	option(client)

	assert.True(t, client.plainHttp)
	assert.True(t, client.insecure)
	assert.Equal(t, "user", client.login)
	assert.Equal(t, "pass", client.password)
}

func TestWithInsecure_DoesNotAffectOtherFields(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")
	client := &Client{
		ref:       parsedRef,
		plainHttp: true,
		login:     "user",
		password:  "pass",
	}

	option := WithInsecure()
	option(client)

	assert.True(t, client.plainHttp)
	assert.True(t, client.insecure)
	assert.Equal(t, "user", client.login)
	assert.Equal(t, "pass", client.password)
}

func TestAllOptions_CombinedEffect(t *testing.T) {
	parsedRef, _ := reference.Parse("cr://registry.example.com/library/image:latest")

	client, err := NewRegistryClient(
		parsedRef,
		WithPlainHttp(),
		WithInsecure(),
		WithCreds("admin", "secret"),
	)
	require.NoError(t, err)

	c := client.(*Client)
	assert.True(t, c.plainHttp)
	assert.True(t, c.insecure)
	assert.Equal(t, "admin", c.login)
	assert.Equal(t, "secret", c.password)
}
