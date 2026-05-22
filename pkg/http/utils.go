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
	"net/http"
	"os"
	"path/filepath"

	"github.com/natefinch/atomic"
)

func ResponseToFile(resp *http.Response, file string) error {
	defer func() { _ = resp.Body.Close() }()

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
			// It is not always possible to obtain a checksum to verify a file
			// skip file
			return nil
		}
	}

	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}

	return atomic.WriteFile(file, resp.Body)
}
