//go:build !fabric_sandbox

package agent

import (
	"errors"

	"github.com/asx8678/ultra/internal/permission"
)

var errFabricSandboxUnavailable = errors.New(
	"experimental Fabric is enabled but this Ultra binary was built without the fabric_sandbox tag",
)

func newFabricRuntime(permission.Service) (fabricRuntime, error) {
	return nil, errFabricSandboxUnavailable
}
