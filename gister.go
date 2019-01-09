package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/leaf-ai/studio-go-runner/pkg/studio"

	"github.com/karlmutch/errors"
	"github.com/karlmutch/stack"
)

const (
	// Version defines the app version
	VERSION = "v0.4.0"

	USER_AGENT = "gister/" + VERSION
)

var (
	logger = studio.NewLogger("gister")

	public      bool
	description string
	anonymous   bool
	update      string
	responseObj map[string]interface{}
)

// The top-level struct for a gist file
type GistFile struct {
	Content string `json:"content"`
}

// The required structure for POST data for API purposes
type Gist struct {
	Description string              `json:"description,omitempty"`
	Public      bool                `json:"public"`
	GistFile    map[string]GistFile `json:"files"`
}

// loadTokenFromFile loads the GISTER_GITHUB_TOKEN from a '$HOME/.gist' file
// from the user's home directory.
func loadToken() (token string, err errors.Error) {
	// GISTER_GITHUB_TOKEN must be in format of `username:token`
	if token = os.Getenv("GISTER_GITHUB_TOKEN"); len(token) != 0 {
		return token, nil
	}

	// Fall back to attempting to read from the config file
	file := filepath.Join(os.Getenv("HOME"), ".gist")
	github_token, errGo := ioutil.ReadFile(file)
	if errGo != nil {
		return "", errors.Wrap(errGo).With("file", file).With("stack", stack.Trace().TrimRuntime())
	}
	return strings.TrimSpace(string(github_token)), nil
}

func getGist(names []string) (gist *Gist, err errors.Error) {

	// create a gist from the files array
	gist = &Gist{
		Description: strings.Join(flag.Args(), ", "),
		Public:      false,
		GistFile:    map[string]GistFile{},
	}

	for _, filename := range names {

		if filename == "-" {
			logger.Debug("Reading standard input")
			content, errGo := ioutil.ReadAll(os.Stdin)
			if errGo != nil {
				return nil, errors.Wrap(errGo).With("file", "-").With("stack", stack.Trace().TrimRuntime())
			}
			uu, errGo := uuid.NewV4()
			if errGo != nil {
				return nil, errors.Wrap(errGo).With("file", "-").With("stack", stack.Trace().TrimRuntime())
			}
			gist.GistFile[uu.String()] = GistFile{string(content)}
			continue
		}

		logger.Debug("Reading file: " + filename)
		content, errGo := ioutil.ReadFile(filename)
		if errGo != nil {
			return nil, errors.Wrap(errGo).With("file", filename).With("stack", stack.Trace().TrimRuntime())
		}
		gist.GistFile[filepath.Base(filename)] = GistFile{string(content)}
	}

	return gist, nil
}

// Defines basic usage when program is run with the help flag
func usage() {
	fmt.Fprintf(os.Stderr, "usage: gist [options] <file>|-\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// The main function parses the CLI args. It also checks the files, and
// loads them into an array.
// Then the files are separated into GistFile structs and collectively
// the files are saved in `files` field in the Gist struct.
// A request is then made to the GitHub api - it depends on whether it is
// anonymous gist or not.
// The response recieved is parsed and the Gist URL is printed to STDOUT.
func main() {
	flag.StringVar(&update, "u", "", "id of existing gist to update")
	flag.BoolVar(&public, "p", false, "Set to true for public gist.")
	flag.BoolVar(&anonymous, "a", false, "Set to true for anonymous gist user")
	flag.StringVar(&description, "d", "", "Description for gist.")
	flag.Usage = usage
	flag.Parse()

	fileNames := flag.Args()
	if len(fileNames) == 0 {
		log.Fatal("Error: No input file(s), or standard input specified.")
	}

	gist, err := getGist(fileNames)
	if err != nil {
		logger.Fatal(err.Error())
	}

	// Override defaults with the command line specified values, if they are not empty in the
	// case of the description
	if len(description) != 0 {
		gist.Description = description
	}
	gist.Public = public

	pfile, errGo := json.Marshal(*gist)
	if errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}

	// Send request to API
	base, errGo := url.Parse("https://api.github.com/")
	if errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}

	postTo := "gists"
	if update != "" {
		postTo += "/" + update
	}
	urlPath, errGo := url.Parse(postTo)
	if errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}

	req, errGo := http.NewRequest("POST", base.ResolveReference(urlPath).String(), bytes.NewBuffer(pfile))
	if errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", USER_AGENT)
	if !anonymous {
		token, err := loadToken()
		if err != nil {
			logger.Fatal(err.Error())
		}
		words := strings.Split(token, ":")
		if len(words) != 2 {
			log.Fatalf("token must be in form 'username:token'")
		}
		req.SetBasicAuth(words[0], words[1])
	}

	logger.Debug("Uploading...")
	client := http.Client{}
	response, errGo := client.Do(req)
	if errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}
	defer response.Body.Close()

	if errGo = json.NewDecoder(response.Body).Decode(&responseObj); errGo != nil {
		logger.Fatal(errors.Wrap(errGo).With("stack", stack.Trace().TrimRuntime()).Error())
	}

	if _, ok := responseObj["html_url"]; !ok {
		if a, ok := responseObj["errors"]; ok {
			for i, m := range a.([]interface{}) {
				for k, v := range m.(map[string]interface{}) {
					logger.Error(fmt.Sprintf("%d %s: %s\n", i, k, v))
				}
			}
		}
		logger.Error(responseObj["message"].(string), "url", base.ResolveReference(urlPath).String())
	}
}
