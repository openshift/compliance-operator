// +build tools

// Place any runtime dependencies as imports in this file.
// Go modules will be forced to download and install them.
package tools

import (
	_ "github.com/securego/gosec/cmd/gosec"
	_ "k8s.io/code-generator/cmd/client-gen"
)
