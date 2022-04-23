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
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/PaesslerAG/jsonpath"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	nodeRolePrefix         = "node-role.kubernetes.io/"
	generatedKubelet       = "generated-kubelet"
	generatedKubeletSuffix = "kubelet"
	mcPayloadPrefix        = `data:text/plain,`
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
		// The prefix has to start with 99 since the kubeletconfig generated machine config will always start with 99
		if strings.HasPrefix(kcName, "99-") && strings.Contains(kcName, generatedKubelet) {
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

func AreKubeletConfigsRendered(pool *mcfgv1.MachineConfigPool, client runtimeclient.Client) (bool, error, string) {
	// find out if pool is using a custom kubelet config
	diffString := ""
	isUsingKC, currentKCMCName, err := IsMcfgPoolUsingKC(pool)
	if err != nil {
		return false, fmt.Errorf("failed to check if pool %s is using a custom kubelet config: %w", pool.Name, err), diffString
	}
	if !isUsingKC || currentKCMCName == "" {
		return true, nil, diffString
	}
	// if the pool is using a custom kubelet config, check if the kubelet config is rendered
	kcmcfg := &mcfgv1.MachineConfig{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: currentKCMCName}, kcmcfg)
	if err != nil {
		return false, fmt.Errorf("failed to get machine config %s: %w", currentKCMCName, err), diffString
	}

	kc, err := GetKCFromMC(kcmcfg, client)
	if err != nil {
		return false, fmt.Errorf("failed to get kubelet config from machine config %s: %w", currentKCMCName, err), diffString
	}

	var obj interface{}
	err = json.Unmarshal(kcmcfg.Spec.Config.Raw, &obj)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal machine config %s: %w", currentKCMCName, err), diffString
	}

	encodedKC, err := jsonpath.Get("storage.files[0].contents.source", obj)
	if err != nil {
		return false, fmt.Errorf("failed to get encoded kubelet config from machine config %s: %w", currentKCMCName, err), diffString
	}
	encodedKCStr := encodedKC.(string)
	if encodedKCStr == "" {
		return false, fmt.Errorf("encoded kubeletconfig %s is empty", currentKCMCName), diffString
	}
	encodedKCStrTrimmed := strings.TrimPrefix(encodedKCStr, mcPayloadPrefix)

	if encodedKCStrTrimmed == encodedKCStr {
		return false, fmt.Errorf("encoded kubeletconfig %s does not contain %s prefix", currentKCMCName, mcPayloadPrefix), diffString
	}

	decodedKC, err := url.PathUnescape(encodedKCStrTrimmed)
	if err != nil {
		return false, fmt.Errorf("failed to decode kubeletconfig %s: %w", currentKCMCName, err), diffString
	}

	isSubset, diff, err := JSONIsSubset(kc.Spec.KubeletConfig.Raw, []byte(decodedKC))
	if err != nil {
		return false, fmt.Errorf("failed to check if kubeletconfig %s is subset of rendered MC %s: %w", kc.Name, currentKCMCName, err), ""
	}
	if isSubset {
		return true, nil, diffString
	} else {
		diffData := make([][]string, 0)
		for _, r := range diff.Rows {
			diffData = append(diffData, []string{fmt.Sprintf("Path: %s", r.Key), fmt.Sprintf("Expected: %s", r.Expected), fmt.Sprintf("Got: %s", r.Got)})
		}
		diffString = fmt.Sprintf("kubeletconfig %s is not subset of rendered MC %s, diff: %v", kc.Name, currentKCMCName, diffData)
		return false, nil, diffString
	}
}

func GetKCFromMC(mc *mcfgv1.MachineConfig, client runtimeclient.Client) (*mcfgv1.KubeletConfig, error) {
	if mc == nil {
		return nil, fmt.Errorf("machine config is nil")
	}
	if len(mc.GetOwnerReferences()) != 0 {
		if mc.GetOwnerReferences()[0].Kind == "KubeletConfig" {
			kubeletName := mc.GetOwnerReferences()[0].Name
			kubeletConfig := &mcfgv1.KubeletConfig{}
			kcKey := types.NamespacedName{Name: kubeletName}
			if err := client.Get(context.TODO(), kcKey, kubeletConfig); err != nil {
				return nil, fmt.Errorf("couldn't get current KubeletConfig: %w", err)
			}
			return kubeletConfig, nil
		}
	}
	return nil, fmt.Errorf("machine config %s doesn't have a KubeletConfig owner reference", mc.GetName())
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
