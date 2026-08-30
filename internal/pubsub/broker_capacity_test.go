package pubsub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultBrokerCapacityIsBounded(t *testing.T) {
	t.Parallel()
	broker := NewBroker[int]()
	require.Equal(t, 256, broker.channelBufferSize)
}

func TestBrokerCapacityCanBeSpecialized(t *testing.T) {
	t.Parallel()
	broker := NewBrokerWithOptions[int](16)
	require.Equal(t, 16, broker.channelBufferSize)
}
