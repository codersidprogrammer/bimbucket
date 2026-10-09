package coop

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// fakeDB is a tiny in-memory Firebase Realtime Database: flat path->JSON store
// with ETag conditional writes and a blocking text/event-stream endpoint.
type fakeDB struct {
	mu    sync.Mutex
	data  map[string]string
	etags map[string]int
	seq   int
}

func newFakeDB() *fakeDB {
	return &fakeDB{data: map[string]string{}, etags: map[string]int{}}
}

func (f *fakeDB) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.handle))
}

func rtdbPath(urlPath string) string {
	p := strings.TrimSuffix(strings.TrimPrefix(urlPath, "/"), ".json")
	return strings.Trim(p, "/")
}

func (f *fakeDB) handle(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-r.Context().Done()
		return
	}
	p := rtdbPath(r.URL.Path)
	switch r.Method {
	case http.MethodGet:
		f.get(w, p)
	case http.MethodPut:
		f.put(w, r, p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeDB) get(w http.ResponseWriter, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.etags[p]; ok {
		w.Header().Set("ETag", strconv.Itoa(e))
	}
	if v, ok := f.data[p]; ok {
		io.WriteString(w, v)
		return
	}
	// Gather one level of children under the prefix.
	prefix := p + "/"
	out := map[string]json.RawMessage{}
	for k, v := range f.data {
		if strings.HasPrefix(k, prefix) {
			rel := k[len(prefix):]
			out[rel] = json.RawMessage(v)
		}
	}
	if len(out) == 0 {
		io.WriteString(w, "null")
		return
	}
	json.NewEncoder(w).Encode(out)
}

func (f *fakeDB) put(w http.ResponseWriter, r *http.Request, p string) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch match := r.Header.Get("if-match"); match {
	case "*":
		if _, ok := f.data[p]; ok {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	case "":
	default:
		if strconv.Itoa(f.etags[p]) != match {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}
	for k := range f.data {
		if strings.HasPrefix(k, p+"/") {
			delete(f.data, k)
		}
	}
	f.data[p] = string(body)
	f.seq++
	f.etags[p] = f.seq
	w.Header().Set("ETag", strconv.Itoa(f.seq))
	w.WriteHeader(http.StatusOK)
}
