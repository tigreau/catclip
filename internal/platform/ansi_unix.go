//go:build darwin || linux

package platform

import "os"

func enableANSIOutput(_ *os.File) bool { return true }
