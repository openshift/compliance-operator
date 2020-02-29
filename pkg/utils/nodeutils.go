/*
Copyright © 2020 Red Hat Inc.

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
package utils

import "strings"

const (
	nodeRolePrefix = "node-role.kubernetes.io/"
)

func GetFirstNodeRole(nodeSelector map[string]string) string {
	if nodeSelector == nil {
		return ""
	}

	// FIXME: should we protect against multiple labels and return
	// an empty string if there are multiple?
	for k := range nodeSelector {
		if strings.HasPrefix(k, nodeRolePrefix) {
			return strings.TrimPrefix(k, nodeRolePrefix)
		}
	}

	return ""
}
