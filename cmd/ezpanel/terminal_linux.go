//go:build linux

package main

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

func terminal() bool { _, e := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS); return e == nil }
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	old, e := unix.IoctlGetTermios(fd, unix.TCGETS)
	if e != nil {
		return "", fmt.Errorf("密码读取需要交互终端")
	}
	next := *old
	next.Lflag &^= unix.ECHO
	if e = unix.IoctlSetTermios(fd, unix.TCSETS, &next); e != nil {
		return "", e
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, old)
	fmt.Print(prompt)
	p, e := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
	return strings.TrimSuffix(strings.TrimSuffix(p, "\n"), "\r"), e
}
