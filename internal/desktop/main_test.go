package desktop

import (
	"os"
	"testing"

	"github.com/spachava753/mu/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.WithoutGH(m.Run)) }
