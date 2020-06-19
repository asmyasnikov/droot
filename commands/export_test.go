package commands

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOutType(t *testing.T) {
	cases := map[string]struct {
		t OutType
		err bool
	}{
		"":        {PIPE, false},
		".":       {DIR, true},
		"out":     {DIR, false},
		"out.tar": {TAR, false},
	}
	for output, expected := range cases {
		oType, err := outType(output)
		if expected.err {
			require.Error(t, err, fmt.Sprintf("out <%s> -> %+v", output, expected))
		} else {
			require.NoError(t, err, fmt.Sprintf("out <%s> -> %+v", output, expected))
		}
		require.Equal(t, expected.t, oType, fmt.Sprintf("out <%s> -> %+v", output, expected))
	}
}
