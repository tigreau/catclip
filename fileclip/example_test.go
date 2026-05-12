package fileclip_test

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/fileclip"
)

func ExampleCopy() {
	// Create a temporary file for the example.
	path := filepath.Join(os.TempDir(), "fileclip-example.txt")
	os.WriteFile(path, []byte("hello from fileclip"), 0644)
	defer os.Remove(path)

	err := fileclip.Copy(path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("File reference copied to clipboard")
	// The file can now be pasted into Finder, Claude, ChatGPT, or Gemini.
}

func ExampleCopy_multiple() {
	dir := os.TempDir()
	pathA := filepath.Join(dir, "fileclip-a.txt")
	pathB := filepath.Join(dir, "fileclip-b.txt")
	os.WriteFile(pathA, []byte("file a"), 0644)
	os.WriteFile(pathB, []byte("file b"), 0644)
	defer os.Remove(pathA)
	defer os.Remove(pathB)

	err := fileclip.Copy(pathA, pathB)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("2 file references copied to clipboard")
}
