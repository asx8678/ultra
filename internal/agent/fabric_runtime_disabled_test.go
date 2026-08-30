//go:build !fabric_sandbox

package agent

import (
	"testing"

	"github.com/asx8678/ultra/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestFabricRuntimeFailsClosedWithoutBuildTag(t *testing.T) {
	t.Parallel()
	runtime, err := newFabricRuntime(permission.NewPermissionService(t.TempDir(), false, nil))
	require.ErrorIs(t, err, errFabricSandboxUnavailable)
	require.Nil(t, runtime)
}
