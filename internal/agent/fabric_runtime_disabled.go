//go:build fabric_disabled

package agent

import (
	"errors"

	"github.com/asx8678/ultra/internal/agent/notify"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/asx8678/ultra/internal/pubsub"
)

var errFabricSandboxUnavailable = errors.New(
	"fabric is enabled but this Ultra binary was built with the fabric_disabled tag",
)

func newFabricRuntime(permission.Service, pubsub.Publisher[notify.Notification]) (fabricRuntime, error) {
	return nil, errFabricSandboxUnavailable
}
