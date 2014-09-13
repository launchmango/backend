package main

import (
	"crypto/md5"
	"encoding/json"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
)

const (
	typeFile = "file"
	typeDir  = "dir"
)

type FileNode struct {
	Type     string      `json:"type"`
	Name     string      `json:"name"`
	Size     int64       `json:"size"`
	Children []*FileNode `json:"children"`
}

type Repository struct {
	ID    string      `json:"id"`
	URL   string      `json:"url"`
	Files []*FileNode `json:"files"`
}

func md5String(s string) string {
	h := md5.New()
	io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func fileExists(filename string) bool {
	if _, err := os.Stat("./" + filename); err != nil {
	   return false
	}
	return true
}

func runCmd(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
	r.HandleFunc("/repositories", createRepo).Methods("POST")
	r.HandleFunc("/repositories/{id}", getRepo).Methods("GET")
	r.HandleFunc("/repositories/{id}/build", buildRepo).Methods("POST")
	http.Handle("/static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir("./static/"))))
	http.Handle("/", r)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	file, err := os.Open("./index.html")
	if err != nil {
		log.Println(err)
		return
	}
	io.Copy(w, file)
}

func createRepo(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var repo Repository
	if err := json.NewDecoder(r.Body).Decode(&repo); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if repo.URL == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	repo.ID = md5String(repo.URL)
	if !fileExists(repo.ID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := runCmd("git", "clone", repo.URL, repo.ID); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	renderJSON(w, http.StatusOK, &repo)
}

func getRepo(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	//if !fileExists(id) {
	//w.WriteHeader(http.StatusNotFound)
	//return
	//}

	visitFunc := func(path string, f os.FileInfo, err error) error {
		if !strings.Contains(path, "/") { // hacky way of not including parent
			return nil
		}
		if strings.Contains(path, ".git") { // don't traverse git
			return nil
		}

		fmt.Printf("Visited: %s\n", path)
		return nil
	}

	filepath.Walk(id, visitFunc)
}

func buildRepo(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	if !fileExists(id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	buf := new(bytes.Buffer)
	cmd := exec.Command("xcodebuild", "-sdk", "iphonesimulator")
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.Dir = id
	err := cmd.Run()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(buf.String())
	}

}
