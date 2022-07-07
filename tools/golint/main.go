package main

import (
	"flag"
	"fmt"
	"os"
	p "path/filepath"
	"strings"
)

var version = `0.3.0`

var (
	verbose     = flag.Bool("v", false, "verbose output")
	showversion = flag.Bool("version", false, "show version information")
)

func verb(f string, a ...interface{}) {
	if *verbose {
		fmt.Fprintf(os.Stderr, f, a...)
	}
}

func main() {
	flag.Parse()
	files := flag.Args()

	if *showversion {
		fmt.Printf("golint %s\n", version)
		return
	}

	verb("Scanning for source files... ")
	if len(files) == 0 {
		// just use the current directory if no files were specified
		files = make([]string, 1)
		files[0] = "."
	}

	srcs := make([]string, 0)
	for _, fname := range files {
		srcs = append(srcs, listFiles(fname, ".go")...)
	}
	verb("\n")

	for _, sf := range srcs {
		f, err := os.Open(sf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s\n", sf)
			continue
		}
		PrintLex(f)
		f.Close()
	}
}

func listFiles(fname string, suf string) (fs []string) {
	info, err := os.Stat(fname)
	if err != nil {
		return
	}
	if info.IsDir() {
		f, _ := os.Open(fname)
		dn, _ := f.Readdirnames(-1)
		for _, filename := range dn {
			if filename[0] == '.' {
				continue
			}
			filename = p.Join(fname, filename)
			info, err := os.Stat(filename)
			if err != nil {
				continue
			}
			if info.IsDir() {
				fs = append(fs, listFiles(filename, suf)...)
			} else if strings.HasSuffix(filename, suf) {
				verb("%s ", filename)
				fs = append(fs, filename)
			}
		}
	} else if strings.HasSuffix(fname, suf) {
		verb("%s", fname)
		fs = append(fs, fname)
	}
	return
}
