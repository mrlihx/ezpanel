package website

import "golang.org/x/sys/unix"

func exchangeSiteDirectories(a, b string) error {
	return unix.RenamexNp(a, b, unix.RENAME_SWAP)
}
