package platform

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestFileLockReleasedWhenPublisherIsKilled(t *testing.T) {
	path := t.TempDir() + "/publication.lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestFileLockPublisherHelper$", "-test.v")
	child.Env = append(os.Environ(), "CATCLIP_TEST_LOCK_FILE="+path)
	pipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	ready := make(chan bool, 1)
	go func() {
		scan := bufio.NewScanner(pipe)
		for scan.Scan() {
			if scan.Text() == "LOCKED" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("publisher did not acquire lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("publisher did not become ready")
	}
	if TryLockFile(f) {
		t.Fatal("contended publication lock was acquired")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if !TryLockFile(f) {
		t.Fatal("terminated publisher left cache permanently locked")
	}
}

func TestFileLockPublisherHelper(t *testing.T) {
	path := os.Getenv("CATCLIP_TEST_LOCK_FILE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !TryLockFile(f) {
		t.Fatal("cannot take helper lock")
	}
	fmt.Println("LOCKED")
	time.Sleep(time.Minute)
}
