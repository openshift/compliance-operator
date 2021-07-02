package utils

import (
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test parsing yaml objects", func() {
	Context("Parsing a single object", func() {
		It("parses the object successfullly", func() {
			const objdef = `---
apiVersion: v1
kind: Service
metadata:
  name: my-service
  namespace: default
spec:
  selector:
  app: MyApp
  ports:
    - protocol: TCP
      port: 80
      targetPort: 9376`

			sr := strings.NewReader(objdef)
			objs, err := ReadObjectsFromYAML(sr)
			Expect(objs).To(HaveLen(1))
			obj := objs[0]
			Expect(err).To(BeNil())
			Expect(obj.GetKind()).To(Equal("Service"))
			Expect(obj.GetName()).To(Equal("my-service"))
			Expect(obj.GetNamespace()).To(Equal("default"))
		})
	})

	Context("Parsing several objects", func() {
		It("parses the objects successfullly", func() {
			const objdef = `---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: default
  name: pod-reader
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: secret-reader
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "watch", "list"]
`

			sr := strings.NewReader(objdef)
			objs, err := ReadObjectsFromYAML(sr)
			Expect(objs).To(HaveLen(2))
			Expect(err).To(BeNil())
			Expect(objs[0].GetKind()).To(Equal("Role"))
			Expect(objs[0].GetName()).To(Equal("pod-reader"))
			Expect(objs[0].GetNamespace()).To(Equal("default"))

			Expect(objs[1].GetKind()).To(Equal("ClusterRole"))
			Expect(objs[1].GetName()).To(Equal("secret-reader"))
		})
	})
})
