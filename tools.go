// +build tools

// Place any runtime dependencies as imports in this file.
// Go modules will be forced to download and install them.
package tools

import (
	_ "github.com/operator-framework/operator-registry/cmd/opm"
	_ "github.com/securego/gosec/cmd/gosec"
)
