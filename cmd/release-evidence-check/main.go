package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runInRepository(".", args, stdout, stderr)
}

func runInRepository(repoDir string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("release-evidence-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "path to the release acceptance manifest")
	releaseTag := flags.String("tag", "", "release tag to validate")
	artifactsDirectory := flags.String("artifacts", "", "optional directory containing release Node binaries to verify")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: release-evidence-check -manifest PATH -tag v0.1.0 [-artifacts DIR]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "release evidence check: unexpected arguments: %v\n", flags.Args())
		flags.Usage()
		return 2
	}
	if *manifestPath == "" || *releaseTag == "" {
		fmt.Fprintln(stderr, "release evidence check: -manifest and -tag are required")
		flags.Usage()
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := validateReleaseEvidence(ctx, repoDir, *manifestPath, *releaseTag)
	if err != nil {
		fmt.Fprintf(stderr, "release evidence check: %v\n", err)
		return 1
	}
	if *artifactsDirectory != "" {
		if err := validateReleaseArtifacts(*artifactsDirectory, result); err != nil {
			fmt.Fprintf(stderr, "release evidence check: release artifacts: %v\n", err)
			return 1
		}
	}

	fmt.Fprintf(stdout, "release evidence check passed for %s (candidate %s)\n", result.ReleaseTag, result.CandidateCommit)
	return 0
}
