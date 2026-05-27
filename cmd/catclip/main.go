package main

import (
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"

	"github.com/tigreau/catclip"
)

func main() {
	// Opt-in profiling, off by default. Each is enabled by pointing its env var
	// at an output path:
	//
	//   CATCLIP_CPUPROFILE=cpu.prof    go tool pprof cpu.prof
	//   CATCLIP_MEMPROFILE=mem.prof    go tool pprof mem.prof   (heap: -inuse_space / -alloc_space)
	//   CATCLIP_TRACE=trace.out        go tool trace trace.out
	//
	// Why a trace, not just CPU: the CPU profile only records on-CPU time. A
	// typical catclip run is mostly *off* CPU — waiting on the rg subprocess,
	// file-read syscalls, fzf, and GC (e.g. the investigated run was only ~37%
	// on-CPU). The execution trace shows that whole timeline, so it is usually
	// the better tool for interactive latency. The heap profile covers both
	// in-use memory and total allocations (GC churn) via pprof's sample types.
	//
	// Caveat: catclip.Main() returns on success but calls os.Exit() on error
	// paths, and os.Exit bypasses deferred funcs — so profiles are written only
	// when Main() returns normally (the success path, e.g. a completed
	// interactive run, which is what we profile). Capturing error paths too would
	// require Main() to return an exit code instead of calling os.Exit().
	defer startProfiling()()

	catclip.Main()
}

// startProfiling starts whichever profilers the CATCLIP_*PROFILE / CATCLIP_TRACE
// env vars request and returns a cleanup func that stops/writes them. The cleanup
// runs on main()'s normal return (see the os.Exit caveat above).
func startProfiling() func() {
	var cleanups []func()

	if path := os.Getenv("CATCLIP_CPUPROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			log.Fatalf("cpuprofile: create %s: %v", path, err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("cpuprofile: start: %v", err)
		}
		cleanups = append(cleanups, func() {
			pprof.StopCPUProfile()
			if err := f.Close(); err != nil {
				log.Printf("cpuprofile: close %s: %v", path, err)
			}
		})
	}

	if path := os.Getenv("CATCLIP_TRACE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			log.Fatalf("trace: create %s: %v", path, err)
		}
		if err := trace.Start(f); err != nil {
			log.Fatalf("trace: start: %v", err)
		}
		cleanups = append(cleanups, func() {
			trace.Stop()
			if err := f.Close(); err != nil {
				log.Printf("trace: close %s: %v", path, err)
			}
		})
	}

	// Heap profile is a snapshot written at cleanup time, after the streaming
	// profilers above are stopped so its forced GC does not pollute them.
	if path := os.Getenv("CATCLIP_MEMPROFILE"); path != "" {
		cleanups = append(cleanups, func() {
			f, err := os.Create(path)
			if err != nil {
				log.Printf("memprofile: create %s: %v", path, err)
				return
			}
			runtime.GC() // up-to-date statistics; reflect live (in-use) memory
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Printf("memprofile: write: %v", err)
			}
			if err := f.Close(); err != nil {
				log.Printf("memprofile: close %s: %v", path, err)
			}
		})
	}

	return func() {
		for _, stop := range cleanups {
			stop()
		}
	}
}
