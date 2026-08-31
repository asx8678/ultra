package anim

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestNoScrambleRendersReadableAnimatedLabelWithSuffix(t *testing.T) {
	t.Parallel()

	a := New(Settings{
		ID:         "thinking",
		Label:      "Thinking",
		NoScramble: true,
		Suffix:     func() string { return "4s" },
	})

	initial := ansi.Strip(a.Render())
	require.Equal(t, "Thinking. 4s", initial)

	for range ellipsisAnimSpeed {
		a.Animate(StepMsg{ID: "thinking"})
	}
	advanced := ansi.Strip(a.Render())
	require.Equal(t, "Thinking.. 4s", advanced)
	require.NotEqual(t, initial, advanced)
}

func TestCalmPulseRendersSlowSingleMovingElement(t *testing.T) {
	t.Parallel()

	a := New(Settings{
		ID:         "calm-thinking",
		Label:      "Thinking",
		NoScramble: true,
		CalmPulse:  true,
		Suffix:     func() string { return "4s" },
	})

	initial := ansi.Strip(a.Render())
	require.Equal(t, "· Thinking 4s", initial)

	for range calmPulseSpeed {
		a.Animate(StepMsg{ID: "calm-thinking"})
	}
	advanced := ansi.Strip(a.Render())
	require.Equal(t, "• Thinking 4s", advanced)
	require.NotContains(t, advanced, "...")
}

func TestAnimationCacheSeparatesScrambleModes(t *testing.T) {
	t.Parallel()

	scrambled := New(Settings{ID: "scrambled", Size: 5, Label: "Working"})
	readable := New(Settings{ID: "readable", Size: 5, Label: "Working", NoScramble: true})

	require.NotEqual(t, settingsHash(Settings{Size: 5, Label: "Working"}), settingsHash(Settings{Size: 5, Label: "Working", NoScramble: true}))
	require.NotEqual(t, settingsHash(Settings{Size: 5, Label: "Working", NoScramble: true}), settingsHash(Settings{Size: 5, Label: "Working", NoScramble: true, CalmPulse: true}))
	require.Greater(t, scrambled.cyclingCharWidth, 0)
	require.Zero(t, readable.cyclingCharWidth)
	require.Equal(t, "Working.", ansi.Strip(readable.Render()))
}
