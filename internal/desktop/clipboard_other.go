//go:build !darwin

package desktop

import "errors"

// ClipboardFile reads a file, image, or text from the clipboard.
// cleanup removes any temporary file yz created. It is always safe to call.
func ClipboardFile() (path string, cleanup func(), err error) {
	return "", func() {}, errors.New("clipboard upload is only available on macOS")
}
