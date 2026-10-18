//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package render

import "os"

func termWidth(*os.File) int { return 0 }
