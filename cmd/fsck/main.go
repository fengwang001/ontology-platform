// Command fsck checks and repairs block filesystem images.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"ontology/fsck"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "format":
		err = cmdFormat(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "repair":
		err = cmdRepair(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(exitCode(err))
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  fsck format <image> [-block-size N] [-blocks N] [-inodes N]
  fsck check  <image>
  fsck repair <image>`)
}

func exitCode(err error) int {
	switch {
	case errors.Is(err, fsck.ErrCorruptHeader):
		return 3
	case errors.Is(err, fsck.ErrRootNotDir):
		return 4
	case errors.Is(err, fsck.ErrRecoveryNameConflict):
		return 5
	case errors.Is(err, fsck.ErrNoFreeInode):
		return 6
	case errors.Is(err, fsck.ErrMounted):
		return 7
	default:
		return 1
	}
}

func cmdFormat(args []string) error {
	fs := flag.NewFlagSet("format", flag.ContinueOnError)
	blockSize := fs.Uint("block-size", 512, "bytes per block")
	blocks := fs.Uint("blocks", 64, "total blocks")
	inodes := fs.Uint("inodes", 16, "inode count")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("format requires an image path")
	}
	img, err := fsck.Format(uint32(*blockSize), uint32(*blocks), uint32(*inodes))
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(0), img.Serialize(), 0o644)
}

func cmdCheck(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("check requires an image path")
	}
	rep, err := fsck.OpenVolume(args[0]).Check()
	if err != nil {
		return err
	}
	fmt.Println(rep.String())
	if !rep.Clean() {
		os.Exit(1)
	}
	return nil
}

func cmdRepair(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("repair requires an image path")
	}
	rep, log, err := fsck.OpenVolume(args[0]).Repair(fsck.RepairOptions{})
	if err != nil {
		return err
	}
	fmt.Println("findings before repair:")
	fmt.Println(rep.String())
	fmt.Println("repair actions:")
	for _, a := range log.Actions {
		fmt.Println("  " + a)
	}
	if log.DataLoss {
		fmt.Println("WARNING: data loss occurred during repair")
	}
	return nil
}
