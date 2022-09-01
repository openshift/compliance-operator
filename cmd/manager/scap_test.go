package manager

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"

	"github.com/ComplianceAsCode/compliance-operator/pkg/controller/common"
	"github.com/ComplianceAsCode/compliance-operator/pkg/utils"
	"github.com/antchfx/xmlquery"
	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/wI2L/jsondiff"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Testing SCAP parsing and storage", func() {
	// Turn to `true` if debugging
	debugLog = true

	Context("Parsing SCAP Content", func() {
		var dataStreamFile *os.File
		var contentDS *xmlquery.Node

		BeforeEach(func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new.xml")
			Expect(err).To(BeNil())
		})
		AfterEach(func() {
			dataStreamFile.Close()
		})

		It("Gets the appropriate resource URIs", func() {
			By("parsing content without errors")
			var err error
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
				{
					ObjPath:  "/api/v1/namespaces/openshift-kube-apiserver/configmaps/config",
					DumpPath: "/api/v1/namespaces/openshift-kube-apiserver/configmaps/config",
				},
			}
			got, _ := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", nil)
			Expect(got).To(Equal(expected))
		})
	})

	Context("Parsing SCAP Content with runtime customization for OCP API resource", func() {
		var dataStreamFile *os.File
		var contentDS *xmlquery.Node

		It("Gets the appropriate resource URIs", func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new-warning-variable.xml")
			Expect(err).To(BeNil())

			By("parsing content without errors")
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
				{
					ObjPath:  "/api/v1/namespaces/master-mycluster1/configmaps/kas-config",
					DumpPath: "/api/v1/namespaces/master-mycluster1/configmaps/kas-config",
					Filter:   ".apiServerArguments",
				},
			}
			got, _ := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", nil)
			Expect(got).To(Equal(expected))

			dataStreamFile.Close()
		})

		It("Gets valid resource URIs even if some of the URL references contain non-existent variable reference", func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new-warning-variable-nonexistent.xml")
			Expect(err).To(BeNil())

			By("parsing content without errors")
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
			}
			got, _ := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", nil)
			Expect(got).To(Equal(expected))
			dataStreamFile.Close()
		})

		It("Gets valid resource URIs even if some of the URL references contain mal-formed go template", func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new-warning-variable-malformed.xml")
			Expect(err).To(BeNil())

			By("parsing content without errors")
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
			}
			got, _ := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", nil)
			Expect(got).To(Equal(expected))
			dataStreamFile.Close()
		})

		It("Gets the appropriate resource URIs customized in a tailored profile", func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new-warning-variable.xml")
			Expect(err).To(BeNil())
			tpDataStreamFile, err := os.Open("../../tests/data/tailored-profile.xml")
			Expect(err).To(BeNil())

			By("parsing base profile content without errors")
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing tailored profile content without errors")
			tpContentDS, err := parseContent(tpDataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
				{
					ObjPath:  "/api/v1/namespaces/customized/configmaps/kas-config",
					DumpPath: "/api/v1/namespaces/customized/configmaps/kas-config",
					Filter:   ".data[\"config.yaml\"] | fromjson | .apiServerArguments",
				},
			}
			_, valuesList := getResourcePaths(tpContentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", nil)
			got, _ := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate", valuesList)
			Expect(got).To(Equal(expected))

			dataStreamFile.Close()
		})
	})

	Context("Parses the save path appropriately", func() {
		It("Parses correctly with the root being '/tmp'", func() {
			root := "/tmp"
			path := "/apis/foo"
			expectedDir := "/tmp/apis"
			expectedFile := "foo"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})

		It("Parses correctly with the root being '/'", func() {
			root := "/"
			path := "/apis/foo/bar"
			expectedDir := "/apis/foo"
			expectedFile := "bar"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})

		It("Parses correctly with the root being '/tmp/foo'", func() {
			root := "/tmp/foo"
			path := "/apis/foo/bar/baz"
			expectedDir := "/tmp/foo/apis/foo/bar"
			expectedFile := "baz"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})
	})
})

var _ = Describe("Testing filtering", func() {
	Context("Filtering namespaces", func() {
		var rawns []byte
		BeforeEach(func() {
			nsFile, err := os.Open("../../tests/data/namespaces.json")
			Expect(err).To(BeNil())
			var readErr error
			rawns, readErr = ioutil.ReadAll(nsFile)
			Expect(readErr).To(BeNil())
		})
		It("filters namespaces appropriately", func() {
			filteredOut, filterErr := filter(context.TODO(), rawns,
				`[.items[] | select((.metadata.name | startswith("openshift") | not) and (.metadata.name | startswith("kube-") | not) and .metadata.name != "default")]`)
			Expect(filterErr).To(BeNil())
			nsArr := []interface{}{}
			unmErr := json.Unmarshal(filteredOut, &nsArr)
			Expect(unmErr).To(BeNil())
			Expect(nsArr).To(HaveLen(2))
		})
	})

	Context("Testing errors", func() {
		It("outputs error if it can't create filter", func() {
			_, filterErr := filter(context.TODO(), []byte{},
				`.items[`)
			Expect(filterErr).ToNot(BeNil())
		})
		Context("Filtering namespaces", func() {
			var rawns []byte
			BeforeEach(func() {
				nsFile, err := os.Open("../../tests/data/namespaces.json")
				Expect(err).To(BeNil())
				var readErr error
				rawns, readErr = ioutil.ReadAll(nsFile)
				Expect(readErr).To(BeNil())
			})

			It("skips extra results", func() {
				_, filterErr := filter(context.TODO(), rawns, `.items[]`)
				Expect(filterErr).Should(MatchError(MoreThanOneObjErr))
			})
		})
	})
})

type notFoundFetcher struct{}

func (ff *notFoundFetcher) Stream(_ context.Context, _ resourceFetcherClients) (io.ReadCloser, error) {
	return nil, errors.NewNotFound(schema.GroupResource{
		Group:    "some group",
		Resource: "some resource",
	}, "some name")
}

var _ = Describe("Testing fetching", func() {
	var (
		fakeClients resourceFetcherClients
	)

	const (
		mcFipsFilter   = `[.items[] | select(.metadata.name | test("^[0-9]{2}-worker-fips$|^[0-9]{2}-master-fips$"))]|map(.spec.fips == true)`
		mcClevisFilter = `[.items[] | select(.metadata.name | test("^[0-9]{2}-worker-fips$|^[0-9]{2}-master-fips$"))]|map(.spec.config.storage.luks[0].clevis != null)`
	)

	Context("handle fetch failures", func() {
		It("fetches and stores 404s", func() {
			fakeDispatcher := func(uri string) resourceStreamer {
				return &notFoundFetcher{}
			}

			files, warnings, err := fetch(context.TODO(),
				fakeDispatcher,
				resourceFetcherClients{},
				[]utils.ResourcePath{{DumpPath: "key"}})

			Expect(err).To(BeNil())
			Expect(files).To(HaveLen(1))
			Expect(string(files["key"])).To(Equal("# kube-api-error=NotFound"))
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(Equal("could not fetch : some resource.some group \"some name\" not found"))
		})
	})
	Context("handle Machine Config fetching", func() {
		var filter string
		var files map[string][]byte
		var warnings []string
		var err error

		JustBeforeEach(func() {
			testDevice := "/dev/test"

			fakeIgn := igntypes.Config{
				Ignition: igntypes.Ignition{
					Version: "3.2.0",
				},
				Storage: igntypes.Storage{
					Luks: []igntypes.Luks{
						{
							Device: &testDevice,
							Clevis: &igntypes.Clevis{},
						},
					},
					Files: []igntypes.File{
						{
							Node: igntypes.Node{
								Path: "/etc/foo",
							},
						},
					},
				},
			}

			rawFakeIgn, err := json.Marshal(fakeIgn)
			Expect(err).To(BeNil())

			mcList := mcfgv1.MachineConfigList{Items: []mcfgv1.MachineConfig{
				{
					// regression test for RHBZ #2117268
					ObjectMeta: metav1.ObjectMeta{
						Name:      "99-no-ign-mc",
						Namespace: common.GetComplianceOperatorNamespace(),
					},
					Spec: mcfgv1.MachineConfigSpec{
						KernelArguments: []string{"audit=1"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "99-worker-fips",
						Namespace: common.GetComplianceOperatorNamespace(),
					},
					Spec: mcfgv1.MachineConfigSpec{
						FIPS: true,
						Config: runtime.RawExtension{
							Raw: rawFakeIgn,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "99-master-fips",
						Namespace: common.GetComplianceOperatorNamespace(),
					},
					Spec: mcfgv1.MachineConfigSpec{
						FIPS: false,
						Config: runtime.RawExtension{
							Raw: rawFakeIgn,
						},
					},
				},
			}}

			scheme := scheme.Scheme
			scheme.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcList, &mcList.Items[0])

			client := fake.NewFakeClientWithScheme(scheme, &mcList)
			fakeClients = resourceFetcherClients{client: client}

			fetchMcResources := []utils.ResourcePath{
				{
					ObjPath:  "/apis/machineconfiguration.openshift.io/v1/machineconfigs",
					Filter:   filter,
					DumpPath: "mcDumpPath",
				},
			}

			files, warnings, err = fetch(context.TODO(), getStreamerFn, fakeClients, fetchMcResources)
		})
		When("MC filters FIPS", func() {
			BeforeEach(func() {
				filter = mcFipsFilter
			})

			It("Keeps the FIPS attributes intact", func() {
				Expect(err).To(BeNil())
				Expect(files).To(HaveLen(1))
				Expect(string(files["mcDumpPath"])).To(Equal("[false,true]"))
				Expect(warnings).To(HaveLen(0))
			})
		})
		When("MC filters Clevis", func() {
			BeforeEach(func() {
				filter = mcClevisFilter
			})

			It("Keeps the Clevis attributes intact", func() {
				Expect(err).To(BeNil())
				Expect(files).To(HaveLen(1))
				Expect(string(files["mcDumpPath"])).To(Equal("[true,true]"))
				Expect(warnings).To(HaveLen(0))
			})
		})
	})

	Context("Test fetching KubeletConfig", func() {
		var fetchedResult map[string][]byte
		var fetchedInconsistentResult map[string][]byte
		var warnings []string
		var err error
		var roleNodesList map[string][]string
		var expectedNodeList map[string][]string
		var expectedFiguredResources []utils.ResourcePath
		var figuredResources []utils.ResourcePath
		var expectedAggregatedResult map[string][]byte
		var expectedInconsistentResult map[string][]byte
		JustBeforeEach(func() {

			// Fake KubeletConfig
			kubeletConfig := []byte(`{
				"enableServer": false,
				"staticPodPath": "/etc/kubernetes/manifests",
				"syncFrequency": "1m0s",
				"fileCheckFrequency": "20s",
				"httpCheckFrequency": "20s",
				"address": "0.0.0.0",
				"port": 10250,
				"tlsCipherSuites": [
				  "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				  "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				  "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
				  "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				  "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
				  "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"
				],
				"tlsMinVersion": "VersionTLS12",
				"rotateCertificates": true,
				"serverTLSBootstrap": true,
				"authentication": {
				  "x509": {
					"clientCAFile": "/etc/kubernetes/kubelet-ca.crt"
				  },
				  "webhook": {
					"enabled": true,
					"cacheTTL": "2m0s"
				  },
				  "anonymous": {
					"enabled": false
				  }
				},
				"authorization": {
				  "mode": "Webhook",
				  "webhook": {
					"cacheAuthorizedTTL": "5m0s",
					"cacheUnauthorizedTTL": "30s"
				  }
				},
				"registryPullQPS": 5,
				"registryBurst": 10,
				"eventRecordQPS": 5,
				"eventBurst": 10,
				"enableDebuggingHandlers": true,
				"healthzPort": 10248,
				"healthzBindAddress": "127.0.0.1",
				"oomScoreAdj": -999,
				"clusterDomain": "cluster.local",
				"clusterDNS": [
				  "172.30.0.10"
				],
				"streamingConnectionIdleTimeout": "4h0m0s",
				"nodeStatusUpdateFrequency": "10s",
				"nodeStatusReportFrequency": "5m0s",
				"nodeLeaseDurationSeconds": 40,
				"imageMinimumGCAge": "2m0s",
				"imageGCHighThresholdPercent": 85,
				"imageGCLowThresholdPercent": 80,
				"volumeStatsAggPeriod": "1m0s",
				"systemCgroups": "/system.slice",
				"cgroupRoot": "/",
				"cgroupsPerQOS": true,
				"cgroupDriver": "systemd",
				"cpuManagerPolicy": "none",
				"serializeImagePulls": false,
				"evictionHard": {
				  "imagefs.available": "15%",
				  "memory.available": "100Mi",
				  "nodefs.available": "10%",
				  "nodefs.inodesFree": "5%"
				},
				"evictionPressureTransitionPeriod": "5m0s",
				"enableControllerAttachDetach": true,
				"makeIPTablesUtilChains": true,
				"iptablesMasqueradeBit": 14,
				"iptablesDropBit": 15,
				"featureGates": {
				  "APIPriorityAndFairness": true,
				  "CSIMigrationAWS": false,
				  "CSIMigrationAzureFile": false,
				  "CSIMigrationGCE": false,
				  "CSIMigrationvSphere": false,
				  "DownwardAPIHugePages": true,
				  "PodSecurity": true,
				  "RotateKubeletServerCertificate": true
				},
				"enableSystemLogHandler": true,
				"shutdownGracePeriod": "0s",
				"shutdownGracePeriodCriticalPods": "0s",
				"enableProfilingHandler": true,
				"enableDebugFlagsHandler": true,
				"seccompDefault": false,
				"memoryThrottlingFactor": 0.8,
				"registerWithTaints": [
				  {
					"key": "node-role.kubernetes.io/master",
					"effect": "NoSchedule"
				  }
				],
				"registerNode": true,
				"kind": "KubeletConfiguration"
			  }
			  `)

			kubeletConfigInconsistent := []byte(`{
				"enableSystemLogHandler": true,
				"shutdownGracePeriod": "1s",
				"shutdownGracePeriodCriticalPods": "3s",
				"enableProfilingHandler": true,
				"enableDebugFlagsHandler": true,
				"seccompDefault": false,
				"memoryThrottlingFactor": 0.8,
				"registerWithTaints": [
				  {
					"key": "node-role.kubernetes.io/master",
					"effect": "NoSchedule"
				  }
				],
				"registerNode": true,
				"kind": "KubeletConfiguration"
			  }
			  `)

			kubeletConfigIntersection := []byte(`{
				"enableSystemLogHandler": true,
				"enableProfilingHandler": true,
				"enableDebugFlagsHandler": true,
				"seccompDefault": false,
				"memoryThrottlingFactor": 0.8,
				"registerWithTaints": [
				  {
					"key": "node-role.kubernetes.io/master",
					"effect": "NoSchedule"
				  }
				],
				"registerNode": true,
				"kind": "KubeletConfiguration"
			  }
			  `)

			// create fake node list
			fakeNodeList := corev1.NodeList{Items: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-master-0",
						Labels: map[string]string{
							"node-role.kubernetes.io/master": "",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-master-1",
						Labels: map[string]string{
							"node-role.kubernetes.io/master": "",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-worker-0",
						Labels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-worker-1",
						Labels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-worker-2",
						Labels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			}}

			// create fake KubeletConfig for each node

			scheme := scheme.Scheme
			scheme.AddKnownTypes(corev1.SchemeGroupVersion, &fakeNodeList, &fakeNodeList.Items[0])

			client := fake.NewFakeClientWithScheme(scheme, &fakeNodeList)
			fakeClients = resourceFetcherClients{client: client}

			expectedFiguredResources = []utils.ResourcePath{
				{
					ObjPath:  "/api/v1/nodes/test-node-master-0/proxy/configz",
					DumpPath: "/kubeletconfig/master/test-node-master-0",
					Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
				},
				{
					ObjPath:  "/api/v1/nodes/test-node-master-1/proxy/configz",
					DumpPath: "/kubeletconfig/master/test-node-master-1",
					Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
				},
				{
					ObjPath:  "/api/v1/nodes/test-node-worker-0/proxy/configz",
					DumpPath: "/kubeletconfig/worker/test-node-worker-0",
					Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
				},
				{
					ObjPath:  "/api/v1/nodes/test-node-worker-1/proxy/configz",
					DumpPath: "/kubeletconfig/worker/test-node-worker-1",
					Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
				},
				{
					ObjPath:  "/api/v1/nodes/test-node-worker-2/proxy/configz",
					DumpPath: "/kubeletconfig/worker/test-node-worker-2",
					Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
				},
			}
			fetchedResult = make(map[string][]byte)
			fetchedInconsistentResult = make(map[string][]byte)
			expectedAggregatedResult = make(map[string][]byte)
			expectedInconsistentResult = make(map[string][]byte)
			for _, resource := range expectedFiguredResources {
				fetchedResult[resource.DumpPath] = kubeletConfig
				if resource.DumpPath == "/kubeletconfig/master/test-node-master-1" {
					fetchedInconsistentResult[resource.DumpPath] = kubeletConfigInconsistent
					expectedInconsistentResult[resource.DumpPath] = kubeletConfigInconsistent
				} else {
					fetchedInconsistentResult[resource.DumpPath] = kubeletConfig
					expectedInconsistentResult[resource.DumpPath] = kubeletConfig
				}
				expectedAggregatedResult[resource.DumpPath] = kubeletConfig
			}
			expectedAggregatedResult["/kubeletconfig/role/worker"] = kubeletConfig
			expectedAggregatedResult["/kubeletconfig/role/master"] = kubeletConfig

			expectedInconsistentResult["/kubeletconfig/role/master"] = kubeletConfigIntersection
			expectedInconsistentResult["/kubeletconfig/role/worker"] = kubeletConfig

			expectedNodeList = map[string][]string{
				"master": {"test-node-master-0", "test-node-master-1"},
				"worker": {"test-node-worker-0", "test-node-worker-1", "test-node-worker-2"},
			}

		})
		When("Fetching NodeList", func() {
			It("Get Expected Node List", func() {
				roleNodesList, err = fetchNodesWithRole(context.Background(), fakeClients.client)
				Expect(err).To(BeNil())
				Expect(roleNodesList["master"]).To(ConsistOf(expectedNodeList["master"]))
				Expect(roleNodesList["worker"]).To(ConsistOf(expectedNodeList["worker"]))
			})

			It("Get expcted KubeletConfig resource path", func() {
				figuredResources = getKubeletConfigResourcePath(roleNodesList)
				Expect(compareResourcePaths(figuredResources, expectedFiguredResources)).To(Equal(true))
			})
		})
		When("Test for consistency after fetching api resource", func() {
			It("Resource is consistent", func() {
				aggregatedResult, warning, err := saveConsistentKubeletResult(fetchedResult, warnings)
				Expect(err).To(BeNil())
				Expect(warning).To(BeNil())
				Expect(compareFetchedResults(aggregatedResult, expectedAggregatedResult)).To(Equal(true))
			})
			It("Resource is not consistent", func() {
				aggregatedResult, warning, err := saveConsistentKubeletResult(fetchedInconsistentResult, warnings)
				Expect(err).To(BeNil())
				Expect(warning[0]).To(ContainSubstring("not consistent"))
				Expect(compareFetchedResults(aggregatedResult, expectedInconsistentResult)).To(Equal(true))
			})
		})

	})
})

// compare resourcePath arrays
func compareResourcePaths(a, b []utils.ResourcePath) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !compareResourcePathsHelper(a[i], b) {
			return false
		}
	}
	return true
}

func compareResourcePathsHelper(a utils.ResourcePath, b []utils.ResourcePath) bool {
	for _, v := range b {
		if a.ObjPath == v.ObjPath && a.DumpPath == v.DumpPath && a.Filter == v.Filter {
			return true
		}
	}
	return false
}

// compare parseResults
func compareFetchedResults(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		diff, err := jsondiff.CompareJSON(b[k], v)
		if err != nil || diff != nil {
			return false
		}
	}
	return true
}
