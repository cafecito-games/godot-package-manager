package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, true, map[string]string{"k": "v"}, func() { buf.WriteString("TEXT") })
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"k": "v"`)
	require.NotContains(t, buf.String(), "TEXT")
}

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, false, map[string]string{"k": "v"}, func() { buf.WriteString("TEXT") })
	require.NoError(t, err)
	require.Equal(t, "TEXT", buf.String())
}
