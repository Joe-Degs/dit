//go:build linux
// +build linux

package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/Joe-Degs/dit"
	"golang.org/x/sys/unix"
)

func udpListen(addr string, network string) (conn *dit.Conn, err error) {
	config := &net.ListenConfig{
		Control: func(net, addr string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// set socket option to let multiple processes to
				// listen on the same port
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

				// set the priority of the socket high to recieve the
				// packets becuase no packets are coming
				// socket priority [low - high] => [1 - 7]
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_PRIORITY, 7)
			})
		},
	}

	if conn, err = dit.ListenConfigConnWithNetwork(context.Background(), config, network, addr); err != nil {
		return nil, err
	}
	return
}

func restartProcess() error {
	proc := "/proc/self/exe"
	return syscall.Exec(proc, os.Args, os.Environ())
}

// dropPrivileges changes the process to run as the specified user
// This should be called after binding to privileged ports but before serving requests
func dropPrivileges(username string) error {
	if username == "" {
		return nil // No privilege dropping requested
	}
	
	// Don't drop privileges if already running as non-root
	if os.Getuid() != 0 {
		return fmt.Errorf("cannot drop privileges: not running as root (current uid=%d)", os.Getuid())
	}
	
	// Look up the user
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user lookup failed for '%s': %w", username, err)
	}
	
	// Parse user ID and group ID
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid user ID '%s' for user '%s': %w", u.Uid, username, err)
	}
	
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid group ID '%s' for user '%s': %w", u.Gid, username, err)
	}
	
	// Set the group ID first (must be done before setuid)
	if err := syscall.Setgid(int(gid)); err != nil {
		return fmt.Errorf("setgid(%d) failed for user '%s': %w", gid, username, err)
	}
	
	// Set the user ID
	if err := syscall.Setuid(int(uid)); err != nil {
		return fmt.Errorf("setuid(%d) failed for user '%s': %w", uid, username, err)
	}
	
	// Verify the privilege drop worked
	if os.Getuid() != int(uid) || os.Getgid() != int(gid) {
		return fmt.Errorf("privilege drop verification failed: expected uid=%d gid=%d, got uid=%d gid=%d", 
			uid, gid, os.Getuid(), os.Getgid())
	}
	
	return nil
}

func createPidfile(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create pidfile %s: %w", path, err)
	}
	
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("pidfile %s is locked by another process", path)
		}
		return nil, fmt.Errorf("failed to lock pidfile %s: %w", path, err)
	}
	
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to write PID to %s: %w", path, err)
	}
	
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to sync pidfile %s: %w", path, err)
	}
	
	return file, nil
}

func removePidfile(file *os.File, path string) {
	if file != nil {
		file.Close()
		os.Remove(path)
	}
}
