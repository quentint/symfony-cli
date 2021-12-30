package php

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/symfony-cli/symfony-cli/util"
)

const DefaultComposerVersion = 2

type ComposerResult struct {
	code  int
	error error
}

func (c ComposerResult) Error() string {
	if c.error != nil {
		return c.error.Error()
	}

	return ""
}

func (c ComposerResult) ExitCode() int {
	return c.code
}

func Composer(dir string, args []string, stdout, stderr, logger io.Writer) ComposerResult {
	e := &Executor{
		Dir:        dir,
		BinName:    "php",
		Stdout:     stdout,
		Stderr:     stderr,
		SkipNbArgs: -1,
	}

	// try to move the "old" bin/composer.phar to the new location
	oldpath := filepath.Join(filepath.Join(util.GetHomeDir(), "bin"), "composer.phar")
	newpath := filepath.Join(filepath.Join(util.GetHomeDir(), "composer"), "composer.phar")
	if _, err := os.Stat(oldpath); err == nil {
		if err := os.MkdirAll(filepath.Dir(newpath), 0755); err == nil {
			os.Rename(oldpath, newpath)
		}
	}

	composerBin := "composer1"
	if composerVersion() == 2 {
		composerBin = "composer2"
	}
	path, err := e.findComposer(composerBin)
	if err != nil || !isComposerPHPScript(path) {
		fmt.Println("  WARNING: Unable to find Composer, downloading one. It is recommended to install Composer yourself at https://getcomposer.org/download/")
		// we don't store it under bin/ to avoid it being found by findComposer as we want to only use it as a fallback
		binDir := filepath.Join(util.GetHomeDir(), "composer")
		if path, err = downloadComposer(binDir); err != nil {
			return ComposerResult{
				code:  1,
				error: errors.Wrap(err, "unable to find composer, get it at https://getcomposer.org/download/"),
			}
		}
	}

	e.Args = append([]string{"php", path}, args...)
	fmt.Fprintf(logger, "  (running %s %s)\n\n", path, strings.TrimSpace(strings.Join(args, " ")))
	ret := e.Execute(false)
	if ret != 0 {
		return ComposerResult{
			code:  ret,
			error: errors.Errorf("unable to run %s %s", path, strings.Join(args, " ")),
		}
	}
	return ComposerResult{}
}

// isComposerPHPScript checks that the composer file is indeed a phar/PHP script (not a .bat file)
func isComposerPHPScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	magicPrefix := []byte("#!/usr/bin/env php")
	byteSlice := make([]byte, len(magicPrefix))
	if _, err := file.Read(byteSlice); err != nil {
		return false
	}
	return bytes.Equal(byteSlice, magicPrefix)
}

func composerVersion() int {
	var lock struct {
		Version string `json:"plugin-api-version"`
	}
	cwd, err := os.Getwd()
	if err != nil {
		return DefaultComposerVersion
	}
	contents, err := ioutil.ReadFile(filepath.Join(cwd, "composer.lock"))
	if err != nil {
		return DefaultComposerVersion
	}
	if err = json.Unmarshal(contents, &lock); err != nil {
		return DefaultComposerVersion
	}
	if strings.HasPrefix(lock.Version, "1.") {
		return 1
	}
	return DefaultComposerVersion
}

func findComposer(extraBin string) (string, error) {
	for _, file := range []string{extraBin, "composer.phar", "composer"} {
		if pharPath, _ := LookPath(file); pharPath != "" {
			// On Windows, we don't want the .bat, but the real composer phar/PHP file
			if strings.HasSuffix(pharPath, ".bat") {
				pharPath = pharPath[:len(pharPath)-4] + ".phar"
			}
			return pharPath, nil
		}
	}

	return "", os.ErrNotExist
}

func downloadComposer(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "composer.phar")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	sig, err := downloadComposerInstallerSignature()
	if err != nil {
		return "", err
	}
	installer, err := downloadComposerInstaller()
	if err != nil {
		return "", err
	}
	h := sha512.New384()
	h.Write(installer)
	sigh := h.Sum(nil)
	sigd := make([]byte, hex.EncodedLen(len(sigh)))
	hex.Encode(sigd, sigh)
	if !bytes.Equal(sigd, sig) {
		return "", errors.New("signature was wrong when downloading Composer; please try again")
	}
	setupPath := filepath.Join(dir, "composer-setup.php")
	ioutil.WriteFile(setupPath, installer, 0666)

	var stdout bytes.Buffer
	e := &Executor{
		Dir:        dir,
		BinName:    "php",
		Args:       []string{"php", setupPath, "--quiet"},
		SkipNbArgs: 1,
		Stdout:     &stdout,
		Stderr:     &stdout,
	}
	ret := e.Execute(false)
	if ret == 1 {
		return "", errors.New("unable to setup Composer")
	}
	if err := os.Chmod(path, 0755); err != nil {
		return "", err
	}
	if err := os.Remove(filepath.Join(dir, "composer-setup.php")); err != nil {
		return "", err
	}

	return path, nil
}

func downloadComposerInstaller() ([]byte, error) {
	resp, err := http.Get("https://getcomposer.org/installer")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}

func downloadComposerInstallerSignature() ([]byte, error) {
	resp, err := http.Get("https://composer.github.io/installer.sig")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}