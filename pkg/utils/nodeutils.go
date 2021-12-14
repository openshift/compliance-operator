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

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	nodeRolePrefix         = "node-role.kubernetes.io/"
	generatedKubelet       = "generated-kubelet"
	generatedKubeletSuffix = "kubelet"
)

func GetFirstNodeRoleLabel(nodeSelector map[string]string) string {
	if nodeSelector == nil {
		return ""
	}

	// FIXME: should we protect against multiple labels and return
	// an empty string if there are multiple?
	for k := range nodeSelector {
		if strings.HasPrefix(k, nodeRolePrefix) {
			return k
		}
	}

	return ""
}

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

// AnyMcfgPoolLabelMatches verifies if the given nodeSelector matches the nodeSelector
// in any of the given MachineConfigPools
func AnyMcfgPoolLabelMatches(nodeSelector map[string]string, poolList *mcfgv1.MachineConfigPoolList) (bool, *mcfgv1.MachineConfigPool) {
	foundPool := &mcfgv1.MachineConfigPool{}
	for i := range poolList.Items {
		if McfgPoolLabelMatches(nodeSelector, &poolList.Items[i]) {
			return true, &poolList.Items[i]
		}
	}
	return false, foundPool
}

// isMcfgPoolUsingKC check if a MachineConfig Pool is using a custom Kubelet Config
// if any custom Kublet Config used, return name of generated latest KC machine config from the custom kubelet config
func IsMcfgPoolUsingKC(pool *mcfgv1.MachineConfigPool) (bool, string, error) {
	maxNum := -1
	// currentKCMC store and find kueblet MachineConfig with larges num at the end, therefore the latest kueblet MachineConfig
	var currentKCMC string
	for i := range pool.Spec.Configuration.Source {
		kcName := pool.Spec.Configuration.Source[i].Name
		if strings.Contains(kcName, generatedKubelet) {
			// First find if there is just one cutom KubeletConfig
			if maxNum == -1 {
				if strings.HasSuffix(kcName, generatedKubeletSuffix) {
					maxNum = 0
					currentKCMC = kcName
					continue
				}
			}

			lastByteNum := kcName[len(kcName)-1:]
			num, err := strconv.Atoi(lastByteNum)
			if err != nil {
				return false, "", fmt.Errorf("string-int convertion error for KC remediation: %w", err)
			}
			if num > maxNum {
				maxNum = num
				currentKCMC = kcName
			}

		}
	}
	// no custom kubelet machine config is found
	if maxNum == -1 {
		return false, currentKCMC, nil
	}

	return true, currentKCMC, nil
}

// McfgPoolLabelMatches verifies if the given nodeSelector matches the given MachineConfigPool's nodeSelector
func McfgPoolLabelMatches(nodeSelector map[string]string, pool *mcfgv1.MachineConfigPool) bool {
	if nodeSelector == nil {
		return false
	}
	// TODO(jaosorior): Make this work with MatchExpression
	return reflect.DeepEqual(nodeSelector, pool.Spec.NodeSelector.MatchLabels)
}

func GetNodeRoleSelector(role string) map[string]string {
	if role == cmpv1alpha1.AllRoles {
		return map[string]string{}
	}
	return map[string]string{
		nodeRolePrefix + role: "",
	}
}
