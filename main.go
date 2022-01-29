package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/google/go-github/v42/github"
)

type configType struct {
	portPath     string
	makefilePath string
	distinfoPath string
	plistPath    string

	force bool
}

var (
	config           configType     = configType{}
	portversionRegex *regexp.Regexp = regexp.MustCompile(`(PORTVERSION=\s+)(\d+)`)
	tagnameRegex     *regexp.Regexp = regexp.MustCompile(`(GH_TAGNAME=\s+)(\w+)`)
)

func getConfig() {
	flag.StringVar(&config.portPath, "omz-port", "", "Path to the ohmyzsh port directory")
	flag.BoolVar(&config.force, "force", false, "Force port update even if no new version is available")
	flag.Parse()

	config.makefilePath = filepath.Join(config.portPath, "Makefile")
	config.distinfoPath = filepath.Join(config.portPath, "distinfo")
	config.plistPath = filepath.Join(config.portPath, "pkg-plist")

	checkFilesExist(config.makefilePath, config.distinfoPath, config.plistPath)
}

func checkFilesExist(files ...string) {
	for _, filePath := range files {
		fileStat, err := os.Stat(filePath)
		if err != nil {
			panic(err)
		}
		if fileStat.IsDir() {
			panic(fmt.Sprintf("%s is a directory, and it must be a file. Please check your omz-port parameter", filePath))
		}
	}
}

type versionInfo struct {
	numericDate int
	sha         string
}

func (vi versionInfo) String() string {
	return fmt.Sprintf("date: %d, sha: %s", vi.numericDate, vi.sha)
}

func getRemoteVersionInfo() versionInfo {
	client := github.NewClient(nil)
	commit, _, err := client.Repositories.GetCommit(context.TODO(), "ohmyzsh", "ohmyzsh", "master", nil)
	if err != nil {
		panic(err)
	}
	year, month, day := commit.GetCommit().GetCommitter().GetDate().UTC().Date()

	info := versionInfo{}

	info.numericDate = year*10000 + int(month)*100 + day
	info.sha = commit.GetSHA()

	return info
}

func getLocalVersionInfo(makefileData []byte) versionInfo {
	matches := portversionRegex.FindSubmatch(makefileData)
	if len(matches) != 3 {
		panic("Can't find PORTVERSION in the Makefile")
	}
	numericDate, err := strconv.Atoi(string(matches[2]))
	if err != nil {
		panic(err)
	}

	matches = tagnameRegex.FindSubmatch(makefileData)
	if len(matches) != 3 {
		panic("Can't find GH_TAGNAME in the Makefile")
	}
	sha := string(matches[2])

	return versionInfo{numericDate: numericDate, sha: sha}
}

func writeModifiedMakefile(makefileData []byte, info versionInfo) {
	makefileData = tagnameRegex.ReplaceAll(makefileData, []byte(fmt.Sprintf("${1}%s", info.sha)))
	makefileData = portversionRegex.ReplaceAll(makefileData, []byte(fmt.Sprintf("${1}%d", info.numericDate)))

	err := os.WriteFile(config.makefilePath, makefileData, 0o644)
	if err != nil {
		panic(err)
	}
}

func runInPortDir(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = config.portPath
	err := cmd.Run()
	if err != nil {
		panic(err)
	}
}

func main() {
	getConfig()

	log.Print("Checking upstream version")
	remoteInfo := getRemoteVersionInfo()

	log.Print("Reading Makefile")
	makefileData, err := os.ReadFile(config.makefilePath)
	if err != nil {
		panic(err)
	}
	log.Print("Checking local version")
	localInfo := getLocalVersionInfo(makefileData)

	log.Printf("Upstream:\t%s", remoteInfo)
	log.Printf("Local:\t%s", localInfo)

	if remoteInfo.numericDate > localInfo.numericDate {
		log.Print("Update is required")
	} else if config.force {
		log.Print("Update is NOT required, continuing anyway because of -force")
	} else {
		log.Print("Update is NOT required, exiting")
		return
	}

	log.Print("Writing modified Makefile")
	writeModifiedMakefile(makefileData, remoteInfo)

	log.Print("Re-creating distinfo")
	err = os.Remove(config.distinfoPath)
	if err != nil {
		panic(err)
	}
	runInPortDir("make", "clean", "fetch", "makesum")

	log.Print("Re-creating plist")
	runInPortDir("make", "stage")
	cmd := exec.Command("make", "makeplist")
	cmd.Dir = config.portPath
	plist, err := cmd.Output()
	if err != nil {
		panic(err)
	}

	// Remove "you must check your plist message"
	plistSplit := bytes.SplitN(plist, []byte("\n"), 2)
	plist = plistSplit[1]

	err = os.WriteFile(config.plistPath, plist, 0o644)
	if err != nil {
		panic(err)
	}

	log.Print("Testing port")
	runInPortDir("make", "clean", "stage", "stage-qa", "check-plist", "package")
	runInPortDir("make", "clean")

	log.Print("All done")
}
