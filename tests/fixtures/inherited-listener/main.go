package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/Liapoldus/pluginprotocol/transport"
)

func serveInherited() error {
	listener, err := transport.ListenInherited()
	if err != nil {
		return err
	}
	defer listener.Close()
	fmt.Fprintln(os.Stdout, "ready")
	connection, err := listener.Accept()
	if err != nil {
		return err
	}
	defer connection.Close()
	_, err = connection.Write([]byte("accepted"))
	return err
}

func exerciseHandoff() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		return fmt.Errorf("not a TCP listener")
	}
	file, err := tcpListener.File()
	if err != nil {
		return err
	}
	defer file.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "child")
	command.ExtraFiles = []*os.File{file}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	defer command.Process.Kill()
	ready := make(chan error, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr == nil && line != "ready\n" {
			readErr = fmt.Errorf("unexpected child state")
		}
		ready <- readErr
	}()
	select {
	case err := <-ready:
		if err != nil {
			return err
		}
	case <-time.After(5 * time.Second):
		return fmt.Errorf("inherited listener child did not become ready")
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	response := make([]byte, len("accepted"))
	if _, err := connection.Read(response); err != nil {
		return err
	}
	if string(response) != "accepted" {
		return fmt.Errorf("unexpected child response")
	}
	if err := command.Wait(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "inherited-listener-accepted")
	return nil
}

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "child" {
		err = serveInherited()
	} else {
		err = exerciseHandoff()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "listener fixture failed")
		os.Exit(1)
	}
}
