package main

import (
	"net"
	"syscall"
)

func enableBroadcast(c *net.UDPConn) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var result error
	e = raw.Control(func(fd uintptr) {
		result = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
	if e != nil {
		return e
	}
	return result
}
