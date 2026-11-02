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

package cmd

import "testing"

func TestDestination(t *testing.T) {
	for _, tc := range []struct {
		arg, repository, want string
		fails                 bool
	}{
		{arg: "oci://host/packs/a:1", want: "oci://host/packs/a:1"},
		{arg: "oci://host:5000", repository: "packs/a:1", want: "oci://host:5000/packs/a:1"},
		{arg: "oci://host/", repository: "packs/a:1", want: "oci://host/packs/a:1"},
		{arg: "oci://host/packs/a:1", repository: "packs/a:1", want: "oci://host/packs/a:1"},
		{arg: "oci://host/packs/b:1", repository: "packs/a:1", fails: true},
		{arg: "oci://host/packs/a:2", repository: "packs/a:1", fails: true},
		{arg: "oci://host", fails: true},
		{arg: "host", repository: "packs/a:1", fails: true},
	} {
		got, err := destination(tc.arg, tc.repository)
		if (err != nil) != tc.fails || got != tc.want {
			t.Errorf("destination(%q, %q) = %q, %v", tc.arg, tc.repository, got, err)
		}
	}
}
