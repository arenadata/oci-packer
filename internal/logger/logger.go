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

package logger

import (
	"github.com/containerd/log"
	"github.com/sirupsen/logrus"
)

func init() {
	log.L.Logger.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "20060102150405",
		FullTimestamp:   true,
	})
}

func New(group string) *logrus.Entry {
	return log.L.WithField("group", group)
}

func SetLevelDebug() {
	log.L.Logger.SetLevel(logrus.DebugLevel)
}

func SetLevelError() {
	log.L.Logger.SetLevel(logrus.ErrorLevel)
}
