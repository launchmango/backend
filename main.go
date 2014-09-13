package main

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/gorilla/mux"
	"github.com/launchmango/backend/httputil"
)

const (
	typeFile = "file"
	typeDir  = "dir"
)

var (
	errNotFound = &httputil.HTTPError{http.StatusNotFound,
		errors.New("not found")}
	regexpMD5 = regexp.MustCompile("[0-9a-f]{32}")
)

type FileNode struct {
	Type     string      `json:"type"`
	Name     string      `json:"name"`
	Size     int64       `json:"size"`
	Children []*FileNode `json:"children,omitempty"`
}

type Repository struct {
	ID    string    `json:"id"`
	URL   string    `json:"url"`
	Files *FileNode `json:"files,omitempty"`
}

type handler func(w http.ResponseWriter, r *http.Request) error

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rv := recover(); rv != nil {
			err := errors.New("handler panic")
			logError(r, err, rv)
			handleError(w, r, http.StatusInternalServerError, err, false)
		}
	}()
	var rb httputil.ResponseBuffer
	err := h(&rb, r)
	if err == nil {
		rb.WriteTo(w)
	} else if e, ok := err.(*httputil.HTTPError); ok {
		if e.Status >= 500 {
			logError(r, err, nil)
		}
		handleError(w, r, e.Status, e.Err, true)
	} else {
		logError(r, err, nil)
		handleError(w, r, http.StatusInternalServerError, err, false)
	}
}

func logError(req *http.Request, err error, rv interface{}) {
	if err != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "Error serving %s: %v\n", req.URL, err)
		if rv != nil {
			fmt.Fprintln(&buf, rv)
			buf.Write(debug.Stack())
		}
		log.Println(buf.String())
	}
}
func handleError(resp http.ResponseWriter, req *http.Request,
	status int, err error, showErrorMsg bool) {
	var data struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	data.Error.Status = status
	if showErrorMsg {
		data.Error.Message = err.Error()
	} else {
		data.Error.Message = http.StatusText(status)
	}
	resp.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp.WriteHeader(status)
	json.NewEncoder(resp).Encode(&data)
}

func md5String(s string) string {
	h := md5.New()
	io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func fileExists(filename string) bool {
	if _, err := os.Stat("./" + filename); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func runCmd(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitRemote(repoPath string) (string, error) {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = repoPath
	u, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(u), nil
}

func loadRepoFiles(repo *Repository) {
	var lastParent *FileNode
	visitFunc := func(path string, f os.FileInfo, err error) error {
		if strings.Contains(path, ".git") { // don't traverse git
			return nil
		}

		fileType := "file"
		if f.IsDir() {
			fileType = "dir"
		}

		node := &FileNode{
			Type: fileType,
			Name: f.Name(),
			Size: f.Size(),
		}

		if node.Type == "dir" && lastParent == nil {
			lastParent = node
			repo.Files = node
		} else if node.Type == "dir" {
			lastParent.Children = append(lastParent.Children, node)
			lastParent = node
		} else {
			lastParent.Children = append(lastParent.Children, node)
		}

		return nil
	}

	filepath.Walk(repo.ID, visitFunc)
}

func renderJSON(w http.ResponseWriter, status int, v interface{}) error {
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	r := mux.NewRouter()
	r.HandleFunc("/", handleRoot).Methods("GET")
	r.Handle("/repositories", handler(createRepo)).Methods("POST")
	r.Handle("/repositories", handler(listRepos)).Methods("GET")
	r.Handle("/repositories/{id}", handler(getRepo)).Methods("GET")
	r.Handle("/repositories/{id}", handler(deleteRepo)).Methods("DELETE")
	r.Handle("/repositories/{id}/build", handler(buildRepo)).Methods("POST")
	r.Handle("/repositories/{id}/run", handler(runRepo)).Methods("GET")
	r.Handle("/repositories/{id}/files/{path}",
		handler(getRepoFile)).Methods("GET")
	r.Handle("/repositories/{id}/files/{path}",
		handler(setRepoFile)).Methods("PUT")
	http.Handle("/static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir("./static/"))))
	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	file, err := os.Open("./index.html")
	if err != nil {
		log.Println(err)
		return
	}
	io.Copy(w, file)
}

func createRepo(w http.ResponseWriter, r *http.Request) error {
	var repo Repository
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&repo); err != nil {
		return &httputil.HTTPError{http.StatusBadRequest, err}
	}

	if repo.URL == "" {
		return &httputil.HTTPError{http.StatusBadRequest,
			errors.New("url is required")}
	}

	repo.ID = md5String(repo.URL)
	if fileExists(repo.ID) {
		return &httputil.HTTPError{http.StatusBadRequest,
			errors.New("repo already exists")}
	}

	if err := runCmd("git", "clone", repo.URL, repo.ID); err != nil {
		return err
	}

	loadRepoFiles(&repo)

	return renderJSON(w, http.StatusOK, &repo)
}

func listRepos(w http.ResponseWriter, r *http.Request) error {
	repos := []*Repository{}

	d, err := os.Open(".")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	fi, err := d.Readdir(-1)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	for _, fi := range fi {
		if fi.Mode().IsDir() {
			if regexpMD5.MatchString(fi.Name()) {
				name, err := gitRemote(fi.Name())
				if err != nil {
					return err
				}

				repos = append(repos, &Repository{ID: fi.Name(), URL: name})
			}
		}
	}

	return renderJSON(w, http.StatusOK, repos)
}

func getRepo(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	if !fileExists(id) {
		return errNotFound
	}

	name, err := gitRemote(id)
	if err != nil {
		return err
	}

	repo := Repository{ID: id, URL: name}
	loadRepoFiles(&repo)

	renderJSON(w, http.StatusOK, &repo)
	return nil
}

func deleteRepo(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	if !fileExists(id) {
		return errNotFound
	}
	if err := os.RemoveAll(id); err != nil {
		return err
	}
	return nil
}

func buildRepo(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	if !fileExists(id) {
		return errNotFound
	}

	buf := new(bytes.Buffer)
	cmd := exec.Command("xcodebuild", "-arch", "i386", "-sdk", "iphonesimulator")
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.Dir = id
	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

func runRepo(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	if !fileExists(id) {
		return errNotFound
	}

	files, _ := ioutil.ReadDir("./" + id)
	var projectName string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".xcodeproj") {
			projectName = strings.TrimSuffix(f.Name(), ".xcodeproj")
			break
		}
	}

	go runCmd("osascript", "trigger_move_simulator.applescript")

	buf := new(bytes.Buffer)
	cmd := exec.Command("ios-sim", "launch",
		"build/Release-iphonesimulator/"+projectName+".app")
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.Dir = id
	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

func getRepoFile(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	path := mux.Vars(r)["path"]
	filePath := fmt.Sprintf("%s/%s", id, path)
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errNotFound
		}

		return err
	}

	io.Copy(w, file)
	return nil
}

func setRepoFile(w http.ResponseWriter, r *http.Request) error {
	id := mux.Vars(r)["id"]
	path := mux.Vars(r)["path"]
	filePath := fmt.Sprintf("%s/%s", id, path)
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errNotFound
		}

		return nil
	}
	defer file.Close()

	defer r.Body.Close()
	body, _ := ioutil.ReadAll(r.Body) // TODO: stream this
	f, _ := file.Stat()
	ioutil.WriteFile(filePath, body, f.Mode())
	return nil
}
