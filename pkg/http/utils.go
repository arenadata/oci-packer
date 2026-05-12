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

package http

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func ResponseToFile(resp *http.Response, file string) error {
	st, err := os.Stat(file)
	notExists := os.IsNotExist(err)
	if err != nil && !notExists {
		return err
	}
	if !notExists {
		if st.IsDir() {
			return fmt.Errorf("%q is a directory", file)
		}

		if st.Size() > 0 && resp.ContentLength == st.Size() {
			// skip file
			return nil
		}
	}

	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}

	out, err := os.Create(file)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	sfx := longestCommonSuffix([]string{resp.Request.URL.Path, file})
	if sfx[0] == '/' {
		sfx = sfx[1:]
	}

	buf := make([]byte, 8<<20)
	_, err = io.CopyBuffer(out, resp.Body, buf)
	return err
}

func longestCommonSuffix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}

	suffix := strs[0]

	for i := 1; i < len(strs); i++ {
		current := strs[i]
		j := 0

		for j < len(suffix) && j < len(current) {
			if suffix[len(suffix)-1-j] != current[len(current)-1-j] {
				break
			}
			j++
		}

		suffix = suffix[len(suffix)-j:]
		if suffix == "" {
			return ""
		}
	}

	return suffix
}
