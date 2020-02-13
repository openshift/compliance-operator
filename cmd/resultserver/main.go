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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"

	"github.com/spf13/cobra"
)

func defineFlags(cmd *cobra.Command) {
	cmd.Flags().String("address", "1.1.1.1", "Server address")
	cmd.Flags().String("port", "8080", "Server port")
	cmd.Flags().String("path", "/", "Content path")
	cmd.Flags().String("owner", "", "Object owner")
}

type config struct {
	Address string
	Port    string
	Path    string
}

func parseConfig(cmd *cobra.Command) *config {
	conf := &config{
		Address: getValidStringArg(cmd, "address"),
		Port:    getValidStringArg(cmd, "port"),
		Path:    getValidStringArg(cmd, "path"),
	}
	return conf
}

func getValidStringArg(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	if val == "" {
		fmt.Fprintf(os.Stderr, "The command line argument '%s' is mandatory.\n", name)
		os.Exit(1)
	}
	return val
}

func main() {
	var srvCmd = &cobra.Command{
		Use:   "resultserver",
		Short: "A tool to receive raw SCAP scan results.",
		Long:  "A tool to receive raw SCAP scan results.",
		Run: func(cmd *cobra.Command, args []string) {
			server(parseConfig(cmd))
		},
	}

	defineFlags(srvCmd)

	if err := srvCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func server(c *config) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		filename := r.Header.Get("X-Report-Name")
		if filename == "" {
			log.Println("Rejecting. No \"X-Report-Name\" header given.")
			http.Error(w, "Missing report name header", 400)
			return
		}
		encoding := r.Header.Get("Content-Encoding")
		extraExtension := encoding
		if encoding != "" && encoding != "bzip2" {
			log.Println("Rejecting. Invalid \"Content-Encoding\" header given.")
			http.Error(w, "invalid content encoding header", 400)
			return
		} else if encoding == "bzip2" {
			extraExtension = "." + extraExtension
		}
		// TODO(jaosorior): Check that content-type is application/xml
		filePath := path.Join(c.Path, filename+".xml"+extraExtension)
		f, err := os.Create(filePath)
		if err != nil {
			log.Printf("Error creating file: %s", filePath)
			http.Error(w, "Error creating file", 500)
			return
		}
		defer f.Close()

		_, err = io.Copy(f, r.Body)
		if err != nil {
			log.Printf("Error writing file %s", filePath)
			http.Error(w, "Error writing file", 500)
			return
		}
		log.Printf("Received file %s", filePath)
	})
	log.Println("Listening...")
	log.Fatal(http.ListenAndServe(c.Address+":"+c.Port, nil))
}
