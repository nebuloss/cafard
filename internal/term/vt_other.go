//go:build !windows

package term

import "io"

// enableVTOutput is a no-op where terminals interpret escapes natively.
func enableVTOutput(io.Writer) func() { return func() {} }
