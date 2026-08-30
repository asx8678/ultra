//go:build fabric_disabled

package agent

import (
	"testing"

	"github.com/asx8678/ultra/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestFabricRuntimeFailsClosedWhenDisabledAtBuildTime(t *testing.T) {
	t.Parallel()
	runtime, err := newFabricRuntime(permission.NewPermissionService(t.TempDir(), false, nil), nil)
	require.ErrorIs(t, err, errFabricSandboxUnavailable)
	require.Nil(t, runtime)
}
