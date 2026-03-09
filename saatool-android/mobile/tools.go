//go:build tools

package mobile

// This blank import keeps golang.org/x/mobile in go.mod so that
// gobind (used internally by gomobile bind) can find it at build time.
import _ "golang.org/x/mobile/bind"
