//go:build !darwin && !linux

package desktop

import "errors"

// ClipboardImage reads a screenshot from the clipboard.
func ClipboardImage() (path string, cleanup func(), err error) {
	return "", func() {}, errors.New("yz: clipboard images are only available on macOS and Linux")
}
