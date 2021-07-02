package utils

import (
	"errors"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// Reads a YAML file and returns an unstructured object from it. This object
// can be taken into use by the dynamic client
func ReadObjectsFromYAML(r io.Reader) ([]*unstructured.Unstructured, error) {
	objs := []*unstructured.Unstructured{}
	dec := k8syaml.NewYAMLToJSONDecoder(r)
	for {
		var obj unstructured.Unstructured
		err := dec.Decode(&obj)
		if err == nil {
			objs = append(objs, &obj)
		} else if errors.Is(err, io.EOF) {
			break
		} else {
			return objs, err
		}
	}
	return objs, nil
}
