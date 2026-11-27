package plugin

import "os"

func openAuthorizedRoot(path string) (*os.Root, error) {
	return os.OpenRoot(path)
}
