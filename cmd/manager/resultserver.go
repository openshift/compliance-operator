/*
Copyright © 2019 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	utils "github.com/openshift/compliance-operator/pkg/utils"
)

var resultServerCmd = &cobra.Command{
	Use:   "resultserver",
	Short: "A tool to receive raw SCAP scan results.",
	Long:  "A tool to receive raw SCAP scan results.",
	Run: func(cmd *cobra.Command, args []string) {
		server(parseResultServerConfig(cmd))
	},
}

func init() {
	rootCmd.AddCommand(resultServerCmd)
	defineResultServerFlags(resultServerCmd)
}

func defineResultServerFlags(cmd *cobra.Command) {
	cmd.Flags().String("address", "1.1.1.1", "Server address")
	cmd.Flags().String("port", "8443", "Server port")
	cmd.Flags().String("path", "/", "Content path")
	cmd.Flags().String("owner", "", "Object owner")
	cmd.Flags().String("scan-index", "", "The current index of the scan")
	cmd.Flags().String("tls-server-cert", "", "Path to the server cert")
	cmd.Flags().String("tls-server-key", "", "Path to the server key")
	cmd.Flags().String("tls-ca", "", "Path to the CA certificate")
	cmd.Flags().Uint16("rotation", 3, "Amount of raw result directories to keep")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

type resultServerConfig struct {
	Address  string
	Port     string
	BasePath string
	Path     string
	Cert     string
	Key      string
	CA       string
	Rotation uint16
}

func parseResultServerConfig(cmd *cobra.Command) *resultServerConfig {
	basePath := getValidStringArg(cmd, "path")
	index := getValidStringArg(cmd, "scan-index")
	rotation, _ := cmd.Flags().GetUint16("rotation")
	conf := &resultServerConfig{
		Address:  getValidStringArg(cmd, "address"),
		Port:     getValidStringArg(cmd, "port"),
		BasePath: basePath,
		Path:     filepath.Join(basePath, index),
		Cert:     getValidStringArg(cmd, "tls-server-cert"),
		Key:      getValidStringArg(cmd, "tls-server-key"),
		CA:       getValidStringArg(cmd, "tls-ca"),
		Rotation: rotation,
	}

	logf.SetLogger(zap.Logger())

	return conf
}

func ensureDir(path string) error {
	err := os.MkdirAll(path, 0750)
	if err != nil && !os.IsExist(err) {
		log.Error(err, "Couldn't ensure directory")
		return err
	}

	return nil
}

func rotateResultDirectories(rootPath string, rotation uint16) error {
	// If rotation is a negative number, we don't rotate
	if rotation == 0 {
		log.Info("Rotation policy set to '0'. No need to rotate.")
		return nil
	}
	dirs := []utils.Directory{}
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Error(err, "Error accessing directory. Prevent panic by handling failure accessing a path", "filepath", path)
			return err
		}
		if path == rootPath {
			// Do nothing on base directory
			return nil
		}
		if strings.Contains(path, "lost+found") {
			// Do nothing on base directory
			log.Info("Rotation: Skipping 'lost+found' directory")
			return filepath.SkipDir
		}
		if info.IsDir() {
			dirs = append(dirs, utils.NewDirectory(path, info))
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		log.Error(err, "Couldn't rotate directories")
		return err
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].CreationTime.After(dirs[j].CreationTime) })
	var lastError error
	// No need to rotate, we're whithin the policy
	if len(dirs) <= int(rotation) {
		return nil
	}
	for _, dir := range dirs[rotation:] {
		log.Info("Removing directory because of rotation policy", "directory", dir.Path)
		err := os.RemoveAll(dir.Path)
		if err != nil {
			lastError = err
		}
	}
	return lastError
}

func server(c *resultServerConfig) {
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	err := ensureDir(c.Path)
	if err != nil {
		log.Error(err, "Error ensuring result path: %s", c.Path)
		os.Exit(1)
	}

	rotateResultDirectories(c.BasePath, c.Rotation)

	caCert, err := ioutil.ReadFile(c.CA)
	if err != nil {
		log.Error(err, "Error reading CA file")
		os.Exit(1)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	// Configures TLS 1.2
	tlsConfig = libgocrypto.SecureTLSConfig(tlsConfig)
	tlsConfig.ClientCAs = caCertPool
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConfig.BuildNameToCertificate()
	server := &http.Server{
		Addr:      c.Address + ":" + c.Port,
		TLSConfig: tlsConfig,
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		filename := r.Header.Get("X-Report-Name")
		if filename == "" {
			log.Info("Rejecting. No \"X-Report-Name\" header given.")
			http.Error(w, "Missing report name header", 400)
			return
		}
		encoding := r.Header.Get("Content-Encoding")
		extraExtension := encoding
		if encoding != "" && encoding != "bzip2" {
			log.Info("Rejecting. Invalid \"Content-Encoding\" header given.")
			http.Error(w, "invalid content encoding header", 400)
			return
		} else if encoding == "bzip2" {
			extraExtension = "." + extraExtension
		}
		// TODO(jaosorior): Check that content-type is application/xml
		filePath := path.Join(c.Path, filename+".xml"+extraExtension)
		f, err := os.Create(filePath)
		if err != nil {
			log.Info("Error creating file", "file-path", filePath)
			http.Error(w, "Error creating file", 500)
			return
		}
		// #nosec
		defer f.Close()

		_, err = io.Copy(f, r.Body)
		if err != nil {
			log.Info("Error writing file", "file-path", filePath)
			http.Error(w, "Error writing file", 500)
			return
		}
		log.Info("Received file", "file-path", filePath)
	})

	log.Info("Listening...")

	go func() {
		err := server.ListenAndServeTLS(c.Cert, c.Key)
		if err != nil && err != http.ErrServerClosed {
			log.Error(err, "Error in result server")
		}
	}()

	<-exit
	log.Info("Server stopped.")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error(err, "Server shutdown failed")
	}

	log.Info("Server exited gracefully")
}
