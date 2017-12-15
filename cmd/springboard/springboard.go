package main

import (
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"net"
	"syscall"
	"fmt"
	"time"
	"strconv"
)

func main() {
	portStr := os.Getenv("SPRINGBOARD_LISTEN_PORT")
	if len(portStr) == 0 {
		log.Fatalf("SPRINGBOARD_LISTEN_PORT not defined")
	}
	listenAddr := fmt.Sprintf("127.0.0.1:%s", portStr)
	log.Printf("listening on %s", listenAddr)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("failed to listen: %s", err);
	}

	delayStr := os.Getenv("SPRINGBOARD_DELAY_SECONDS")
	if delayStr != "" {
		delay, err := strconv.Atoi(delayStr)
		if err != nil {
			log.Fatalf("failed to parse SPRINGBOARD_DELAY_SECONDS: %s", err);
		}
		log.Infof("sleeping for %d seconds", delay)
		time.Sleep(time.Duration(delay) * time.Second)
	}

	argsWithoutProg := os.Args[1:]
	prog := argsWithoutProg[0]
	args := argsWithoutProg[1:]
	cmd := exec.Command(prog, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		log.Fatalf("failed to start process: %s", err);
	}
	log.Printf("child process pid is %d", cmd.Process.Pid)
	conn, err := ln.Accept()
	if err != nil {
		log.Fatalf("failed to accept connection: %s", err);
	}
	conn.Close()
	log.Printf("accepted connection. Terminating child process...")
	cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	if err != nil {
		log.Warnf("failed to wait for child process: %s", err);
	}
	log.Printf("child success indicator: %t", cmd.ProcessState.Success())
}


